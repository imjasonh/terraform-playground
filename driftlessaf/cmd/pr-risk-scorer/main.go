package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"chainguard.dev/driftlessaf/agents/executor/claudeexecutor"
	"chainguard.dev/driftlessaf/reconcilers/githubreconciler"
	"chainguard.dev/driftlessaf/reconcilers/githubreconciler/statusmanager"
	"chainguard.dev/driftlessaf/workqueue"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/chainguard-dev/clog"
	_ "github.com/chainguard-dev/clog/gcp/init"
	"github.com/chainguard-dev/terraform-infra-common/pkg/httpmetrics"
	"github.com/google/go-github/v75/github"
	"github.com/sethvargo/go-envconfig"
	"google.golang.org/grpc"

	"github.com/imjasonh/terraform-playground/driftlessaf/internal/risk"
)

const reconcilerIdentity = "pr-risk-scorer"

// ReconcilerDetails contains the state tracked by the status manager.
type ReconcilerDetails struct {
	RiskLevel   string   `json:"riskLevel,omitempty"`
	Reasoning   string   `json:"reasoning,omitempty"`
	RiskyFiles  []string `json:"riskyFiles,omitempty"`
	RiskFactors []string `json:"riskFactors,omitempty"`
	Confidence  float64  `json:"confidence,omitempty"`
}

// Markdown renders the details for display in the GitHub Check Run.
func (d ReconcilerDetails) Markdown() string {
	var sb strings.Builder

	sb.WriteString("## Risk Assessment\n\n")

	// Display risk level with emoji
	var riskEmoji string
	switch d.RiskLevel {
	case "high":
		riskEmoji = "🚨"
	case "medium":
		riskEmoji = "⚠️"
	default:
		riskEmoji = "✅"
	}

	fmt.Fprintf(&sb, "%s **Risk Level: %s**\n\n", riskEmoji, strings.ToUpper(d.RiskLevel))

	if d.Confidence > 0 {
		fmt.Fprintf(&sb, "*Confidence: %.0f%%*\n\n", d.Confidence*100)
	}

	if d.Reasoning != "" {
		sb.WriteString("### Assessment\n\n")
		sb.WriteString(d.Reasoning + "\n\n")
	}

	if len(d.RiskFactors) > 0 {
		sb.WriteString("### Risk Factors\n\n")
		for _, factor := range d.RiskFactors {
			fmt.Fprintf(&sb, "- %s\n", factor)
		}
		sb.WriteString("\n")
	}

	if len(d.RiskyFiles) > 0 {
		sb.WriteString("### High-Risk Files\n\n")
		for _, file := range d.RiskyFiles {
			fmt.Fprintf(&sb, "- `%s`\n", file)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

type envConfig struct {
	Port             int    `env:"PORT,default=8080"`
	GithubAppID      int64  `env:"GITHUB_APP_ID,required"`
	GithubPrivateKey string `env:"GITHUB_PRIVATE_KEY,required"`
	GCPProjectID     string `env:"GCP_PROJECT_ID"`
	GCPRegion        string `env:"GCP_REGION,default=us-east5"`
	ClaudeModel      string `env:"CLAUDE_MODEL,default=claude-sonnet-4@20250514"`
}

func main() {
	ctx := context.Background()
	log := clog.FromContext(ctx)

	var cfg envConfig
	envconfig.MustProcess(ctx, &cfg)

	privateKey := []byte(cfg.GithubPrivateKey)

	// Create an App-level transport for looking up installations
	appTransport, err := ghinstallation.NewAppsTransport(
		httpmetrics.WrapTransport(http.DefaultTransport),
		cfg.GithubAppID,
		privateKey,
	)
	if err != nil {
		log.Fatalf("failed to create GitHub app transport: %v", err)
	}
	appClient := github.NewClient(&http.Client{Transport: appTransport})

	// Create status manager for tracking reconciliation state
	statusMgr, err := statusmanager.NewStatusManager[ReconcilerDetails](ctx, reconcilerIdentity)
	if err != nil {
		log.Fatalf("failed to create status manager: %v", err)
	}

	// Create the risk assessment agent
	agent, err := risk.NewAgent(ctx, risk.AgentConfig{
		GCPProjectID: cfg.GCPProjectID,
		GCPRegion:    cfg.GCPRegion,
		Model:        cfg.ClaudeModel,
	})
	if err != nil {
		log.Fatalf("failed to create risk assessment agent: %v", err)
	}

	reconciler := &PRReconciler{
		appID:      cfg.GithubAppID,
		privateKey: privateKey,
		appClient:  appClient,
		clients:    make(map[int64]*github.Client),
		transports: make(map[int64]*ghinstallation.Transport),
		statusMgr:  statusMgr,
		agent:      agent,
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	workqueue.RegisterWorkqueueServiceServer(grpcServer, reconciler)

	// ServeMetrics must run in a goroutine - it blocks!
	go httpmetrics.ServeMetrics()
	defer httpmetrics.SetupTracer(ctx)()

	log.Infof("starting gRPC server on port %d with model %s", cfg.Port, cfg.ClaudeModel)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

type PRReconciler struct {
	workqueue.UnimplementedWorkqueueServiceServer

	appID      int64
	privateKey []byte
	appClient  *github.Client
	statusMgr  *statusmanager.StatusManager[ReconcilerDetails]
	agent      claudeexecutor.Interface[*risk.PRContext, *risk.RiskAssessment]

	mu         sync.RWMutex
	clients    map[int64]*github.Client
	transports map[int64]*ghinstallation.Transport
}

// getClientForRepo returns a GitHub client authenticated for the installation
// that has access to the given owner/repo.
func (r *PRReconciler) getClientForRepo(ctx context.Context, owner, repo string) (*github.Client, error) {
	log := clog.FromContext(ctx)

	// Look up which installation has access to this repo
	installation, _, err := r.appClient.Apps.FindRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("finding installation for %s/%s: %w", owner, repo, err)
	}

	installationID := installation.GetID()

	// Check cache first
	r.mu.RLock()
	client, ok := r.clients[installationID]
	r.mu.RUnlock()
	if ok {
		return client, nil
	}

	// Create new client for this installation
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if client, ok := r.clients[installationID]; ok {
		return client, nil
	}

	itr, err := ghinstallation.New(
		httpmetrics.WrapTransport(http.DefaultTransport),
		r.appID,
		installationID,
		r.privateKey,
	)
	if err != nil {
		return nil, fmt.Errorf("creating installation transport: %w", err)
	}

	client = github.NewClient(&http.Client{Transport: itr})
	r.clients[installationID] = client
	r.transports[installationID] = itr

	log.Infof("created new client for installation %d", installationID)
	return client, nil
}

// prURLRegex matches GitHub PR URLs like https://github.com/owner/repo/pull/123
var prURLRegex = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/pull/(\d+)$`)

func parsePRURL(url string) (owner, repo string, number int, err error) {
	matches := prURLRegex.FindStringSubmatch(url)
	if matches == nil {
		return "", "", 0, fmt.Errorf("invalid PR URL: %s", url)
	}
	number, err = strconv.Atoi(matches[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number in URL %s: %w", url, err)
	}
	return matches[1], matches[2], number, nil
}

func (r *PRReconciler) Process(ctx context.Context, req *workqueue.ProcessRequest) (*workqueue.ProcessResponse, error) {
	log := clog.FromContext(ctx).With("key", req.Key)

	owner, repo, number, err := parsePRURL(req.Key)
	if err != nil {
		log.Warnf("skipping unknown key format: %v", err)
		return &workqueue.ProcessResponse{}, nil
	}

	log = log.With("owner", owner, "repo", repo, "number", number)
	ctx = clog.WithLogger(ctx, log)

	gh, err := r.getClientForRepo(ctx, owner, repo)
	if err != nil {
		log.Errorf("failed to get GitHub client: %v", err)
		return nil, err
	}

	pr, _, err := gh.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		log.Errorf("failed to fetch PR: %v", err)
		return nil, err
	}

	// Add SHA to logger context for Cloud Logging filtering
	sha := pr.GetHead().GetSHA()
	log = log.With("sha", sha)
	ctx = clog.WithLogger(ctx, log)

	// Skip closed PRs
	if pr.GetState() != "open" {
		log.Infof("skipping closed PR")
		return &workqueue.ProcessResponse{}, nil
	}

	log.Infof("processing PR: title=%q state=%s author=%s",
		pr.GetTitle(), pr.GetState(), pr.GetUser().GetLogin())

	// Create status manager session for this PR
	resource := &githubreconciler.Resource{
		Type:  githubreconciler.ResourceTypePullRequest,
		Owner: owner,
		Repo:  repo,
		URL:   req.Key,
		Ref:   pr.GetBase().GetRef(),
	}
	session := r.statusMgr.NewSession(gh, resource, sha)

	// Check existing state for this SHA
	existingStatus, err := session.ObservedState(ctx)
	if err != nil {
		log.Warnf("failed to get observed state: %v", err)
		// Continue anyway - we'll create a new check run
	}

	// Skip if we've already processed this SHA
	if existingStatus != nil && existingStatus.ObservedGeneration == sha {
		log.Infof("already processed SHA %s, skipping", sha[:8])
		return &workqueue.ProcessResponse{}, nil
	}

	// Set in-progress status
	if err := session.SetActualState(ctx, "Assessing risk...", &statusmanager.Status[ReconcilerDetails]{
		Status: "in_progress",
	}); err != nil {
		log.Warnf("failed to set in-progress status: %v", err)
	}

	// Get changed files
	files, _, err := gh.PullRequests.ListFiles(ctx, owner, repo, number, &github.ListOptions{PerPage: 100})
	if err != nil {
		log.Errorf("failed to list files: %v", err)
		return nil, fmt.Errorf("listing files: %w", err)
	}

	// Build file list and calculate totals
	filesChanged := make([]string, 0, len(files))
	var additions, deletions int
	for _, f := range files {
		filesChanged = append(filesChanged, f.GetFilename())
		additions += f.GetAdditions()
		deletions += f.GetDeletions()
	}

	// Build PR context for the agent
	prContext := &risk.PRContext{
		Owner:        owner,
		Repo:         repo,
		PRNumber:     number,
		Title:        pr.GetTitle(),
		Description:  pr.GetBody(),
		Author:       pr.GetUser().GetLogin(),
		FilesChanged: filesChanged,
		Additions:    additions,
		Deletions:    deletions,
	}

	// Execute the risk assessment agent
	log.Infof("running risk assessment agent on %d files (%d additions, %d deletions)",
		len(filesChanged), additions, deletions)

	assessment, err := r.agent.Execute(ctx, prContext, nil)
	if err != nil {
		log.Errorf("risk assessment failed: %v", err)
		return nil, fmt.Errorf("risk assessment: %w", err)
	}

	log.Infof("risk assessment complete: level=%s confidence=%.2f",
		assessment.RiskLevel, assessment.Confidence)

	details := ReconcilerDetails{
		RiskLevel:   assessment.RiskLevel,
		Reasoning:   assessment.Reasoning,
		RiskyFiles:  assessment.RiskyFiles,
		RiskFactors: assessment.RiskFactors,
		Confidence:  assessment.Confidence,
	}

	// Apply risk label
	riskLabel := fmt.Sprintf("risk/%s", assessment.RiskLevel)
	if !hasLabel(pr.Labels, riskLabel) {
		log.Infof("adding risk label: %s", riskLabel)
		_, _, err = gh.Issues.AddLabelsToIssue(ctx, owner, repo, number, []string{riskLabel})
		if err != nil {
			log.Warnf("failed to add label: %v", err)
		}
	}

	// Remove old risk labels
	for _, label := range pr.Labels {
		name := label.GetName()
		if strings.HasPrefix(name, "risk/") && name != riskLabel {
			log.Infof("removing old risk label: %s", name)
			_, err = gh.Issues.RemoveLabelForIssue(ctx, owner, repo, number, name)
			if err != nil {
				log.Warnf("failed to remove label %s: %v", name, err)
			}
		}
	}

	// Determine conclusion and status based on risk level
	status := "completed"
	var conclusion, summary string

	switch assessment.RiskLevel {
	case "high":
		conclusion = "failure"
		summary = "⚠️ High risk changes detected - careful review required"
	case "medium":
		conclusion = "neutral"
		summary = "Medium risk changes - review recommended"
	default:
		conclusion = "success"
		summary = "Low risk changes"
	}

	// Set final status
	if err := session.SetActualState(ctx, summary, &statusmanager.Status[ReconcilerDetails]{
		ObservedGeneration: sha,
		Status:             status,
		Conclusion:         conclusion,
		Details:            details,
	}); err != nil {
		log.Warnf("failed to set final status: %v", err)
	}

	log.Infof("risk assessment complete: level=%s conclusion=%s", assessment.RiskLevel, conclusion)

	return &workqueue.ProcessResponse{}, nil
}

func hasLabel(labels []*github.Label, name string) bool {
	for _, l := range labels {
		if l.GetName() == name {
			return true
		}
	}
	return false
}

func (r *PRReconciler) GetKeyState(ctx context.Context, req *workqueue.GetKeyStateRequest) (*workqueue.KeyState, error) {
	return &workqueue.KeyState{}, nil
}
