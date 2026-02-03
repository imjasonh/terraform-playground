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

	"github.com/imjasonh/terraform-playground/driftlessaf/internal/autolabeler"
)

const reconcilerIdentity = "pr-auto-labeler"

// AutoLabelDetails contains the state tracked by the status manager.
type AutoLabelDetails struct {
	LabelsApplied []string `json:"labelsApplied"`
	FilesAnalyzed int      `json:"filesAnalyzed"`
	TotalChanges  int      `json:"totalChanges"`
}

// Markdown renders the details for display in the GitHub Check Run.
func (d AutoLabelDetails) Markdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Analyzed **%d files** with **%d lines** changed.\n\n", d.FilesAnalyzed, d.TotalChanges))

	if len(d.LabelsApplied) > 0 {
		sb.WriteString("### Labels Applied\n\n")
		for _, label := range d.LabelsApplied {
			sb.WriteString(fmt.Sprintf("- `%s`\n", label))
		}
	} else {
		sb.WriteString("_No labels matched the changed files._\n")
	}

	return sb.String()
}

type envConfig struct {
	Port             int    `env:"PORT, default=8080"`
	GithubAppID      int64  `env:"GITHUB_APP_ID, required"`
	GithubPrivateKey string `env:"GITHUB_PRIVATE_KEY, required"`
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
	statusMgr, err := statusmanager.NewStatusManager[AutoLabelDetails](ctx, reconcilerIdentity)
	if err != nil {
		log.Fatalf("failed to create status manager: %v", err)
	}

	reconciler := &PRReconciler{
		appID:      cfg.GithubAppID,
		privateKey: privateKey,
		appClient:  appClient,
		clients:    make(map[int64]*github.Client),
		statusMgr:  statusMgr,
		labeler:    autolabeler.NewWithDefaults(),
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

	log.Infof("starting gRPC server on port %d", cfg.Port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

type PRReconciler struct {
	workqueue.UnimplementedWorkqueueServiceServer

	appID      int64
	privateKey []byte
	appClient  *github.Client
	statusMgr  *statusmanager.StatusManager[AutoLabelDetails]
	labeler    *autolabeler.Labeler

	mu      sync.RWMutex
	clients map[int64]*github.Client
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
	number, _ = strconv.Atoi(matches[3])
	return matches[1], matches[2], number, nil
}

func (r *PRReconciler) Process(ctx context.Context, req *workqueue.ProcessRequest) (*workqueue.ProcessResponse, error) {
	log := clog.FromContext(ctx).With("key", req.Key)

	owner, repo, number, err := parsePRURL(req.Key)
	if err != nil {
		log.Warnf("skipping non-PR key: %v", err)
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

	// Check if we've already processed this SHA
	existingStatus, err := session.ObservedState(ctx)
	if err != nil {
		log.Warnf("failed to get observed state: %v", err)
		// Continue anyway - we'll create a new check run
	}

	if existingStatus != nil && existingStatus.ObservedGeneration == sha {
		log.Infof("already processed, skipping")
		return &workqueue.ProcessResponse{}, nil
	}

	// Set in-progress status
	if err := session.SetActualState(ctx, "Analyzing changed files...", &statusmanager.Status[AutoLabelDetails]{
		Status: "in_progress",
	}); err != nil {
		log.Warnf("failed to set in-progress status: %v", err)
	}

	// Get changed files
	files, _, err := gh.PullRequests.ListFiles(ctx, owner, repo, number, &github.ListOptions{PerPage: 100})
	if err != nil {
		log.Errorf("failed to fetch changed files: %v", err)
		return nil, err
	}

	// Calculate labels using the autolabeler package
	result := r.labeler.CalculateLabels(files)

	// Filter to only labels we need to add
	newLabels := autolabeler.FilterNewLabels(result.Labels, pr.Labels)

	details := AutoLabelDetails{
		LabelsApplied: result.Labels,
		FilesAnalyzed: result.FilesAnalyzed,
		TotalChanges:  result.TotalChanges,
	}

	// Apply new labels if any
	if len(newLabels) > 0 {
		log.Infof("adding labels: %v", newLabels)
		_, _, err = gh.Issues.AddLabelsToIssue(ctx, owner, repo, number, newLabels)
		if err != nil {
			log.Errorf("failed to add labels: %v", err)
			// Set failure status
			if statusErr := session.SetActualState(ctx, "Failed to apply labels", &statusmanager.Status[AutoLabelDetails]{
				ObservedGeneration: sha,
				Status:             "completed",
				Conclusion:         "failure",
				Details:            details,
			}); statusErr != nil {
				log.Warnf("failed to set failure status: %v", statusErr)
			}
			return nil, err
		}
	} else {
		log.Infof("no new labels to add (calculated: %v)", result.Labels)
	}

	// Set success status
	if err := session.SetActualState(ctx, "Labels applied successfully", &statusmanager.Status[AutoLabelDetails]{
		ObservedGeneration: sha,
		Status:             "completed",
		Conclusion:         "success",
		Details:            details,
	}); err != nil {
		log.Warnf("failed to set success status: %v", err)
	}

	log.Infof("auto-labeling complete: applied=%v files=%d changes=%d",
		result.Labels, result.FilesAnalyzed, result.TotalChanges)

	return &workqueue.ProcessResponse{}, nil
}

func (r *PRReconciler) GetKeyState(ctx context.Context, req *workqueue.GetKeyStateRequest) (*workqueue.KeyState, error) {
	return &workqueue.KeyState{}, nil
}
