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

	"github.com/imjasonh/terraform-playground/driftlessaf/internal/autolabeler"
	"github.com/imjasonh/terraform-playground/driftlessaf/internal/cifixer"
)

const reconcilerIdentity = "pr-reconciler"

// ReconcilerDetails contains the state tracked by the status manager.
type ReconcilerDetails struct {
	// Auto-labeler results
	LabelsApplied []string `json:"labelsApplied,omitempty"`
	FilesAnalyzed int      `json:"filesAnalyzed,omitempty"`
	TotalChanges  int      `json:"totalChanges,omitempty"`

	// CI fixer results
	CIFixAttempted  bool     `json:"ciFixAttempted,omitempty"`
	CIFixSuccess    bool     `json:"ciFixSuccess,omitempty"`
	CIFixFiles      []string `json:"ciFixFiles,omitempty"`
	CIFixReasoning  string   `json:"ciFixReasoning,omitempty"`
	CIFixTurns      int      `json:"ciFixTurns,omitempty"`
	CIFixNeedsHuman bool     `json:"ciFixNeedsHuman,omitempty"`
	CIFixCommit     string   `json:"ciFixCommit,omitempty"`
	CIFixPending    bool     `json:"ciFixPending,omitempty"` // CI checks still running
}

// Markdown renders the details for display in the GitHub Check Run.
func (d ReconcilerDetails) Markdown() string {
	var sb strings.Builder

	// Auto-labeler section
	if d.FilesAnalyzed > 0 {
		sb.WriteString("## Auto-Labeler\n\n")
		fmt.Fprintf(&sb, "Analyzed **%d files** with **%d lines** changed.\n\n", d.FilesAnalyzed, d.TotalChanges)

		if len(d.LabelsApplied) > 0 {
			sb.WriteString("### Labels Applied\n\n")
			for _, label := range d.LabelsApplied {
				fmt.Fprintf(&sb, "- `%s`\n", label)
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("_No labels matched the changed files._\n\n")
		}
	}

	// CI fixer section - show if any CI fixer activity occurred
	if d.CIFixAttempted || d.CIFixPending || d.CIFixSuccess {
		sb.WriteString("## CI Fixer\n\n")

		if d.CIFixPending {
			// CI is still running, waiting to evaluate
			sb.WriteString("⏳ **Waiting for CI to complete**\n\n")
			fmt.Fprintf(&sb, "%s\n\n", d.CIFixReasoning)
		} else if d.CIFixNeedsHuman {
			// Agent determined human intervention is needed
			sb.WriteString("⚠️ **Human intervention requested**\n\n")
			fmt.Fprintf(&sb, "Reason: %s\n\n", d.CIFixReasoning)
			if d.CIFixTurns > 0 {
				fmt.Fprintf(&sb, "Turns attempted: %d\n\n", d.CIFixTurns)
			}
		} else if d.CIFixSuccess {
			// CI is passing
			if d.CIFixTurns > 0 {
				// We made fixes and CI now passes
				sb.WriteString("✅ **CI passing after fix**\n\n")
				if len(d.CIFixFiles) > 0 {
					sb.WriteString("### Files Modified\n\n")
					for _, f := range d.CIFixFiles {
						fmt.Fprintf(&sb, "- `%s`\n", f)
					}
					sb.WriteString("\n")
				}
				if d.CIFixCommit != "" {
					fmt.Fprintf(&sb, "Commit: `%s`\n\n", d.CIFixCommit)
				}
				fmt.Fprintf(&sb, "Turns used: %d\n\n", d.CIFixTurns)
			} else {
				// CI was already passing, no fixes needed
				sb.WriteString("✅ **CI is passing**\n\n")
				sb.WriteString("No fixes were needed.\n\n")
			}
		} else if d.CIFixAttempted {
			// We attempted fixes but CI still failing
			sb.WriteString("❌ **Could not fix CI failure**\n\n")
			fmt.Fprintf(&sb, "Reason: %s\n\n", d.CIFixReasoning)
			if d.CIFixTurns > 0 {
				fmt.Fprintf(&sb, "Turns attempted: %d\n\n", d.CIFixTurns)
			}
		}
	}

	return sb.String()
}

type envConfig struct {
	Port             int    `env:"PORT, default=8080"`
	GithubAppID      int64  `env:"GITHUB_APP_ID, required"`
	GithubPrivateKey string `env:"GITHUB_PRIVATE_KEY, required"`

	// CI Fixer configuration
	EnableCIFixer bool   `env:"ENABLE_CI_FIXER, default=false"`
	GCPProjectID  string `env:"GCP_PROJECT_ID"`
	GCPRegion     string `env:"GCP_REGION, default=us-central1"`
	ClaudeModel   string `env:"CLAUDE_MODEL, default=claude-sonnet-4@20250514"`
	MaxTurns      int    `env:"MAX_TURNS, default=3"`
	CIFixerLabel  string `env:"CI_FIXER_LABEL, default=ci-autofix"`
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

	reconciler := &PRReconciler{
		appID:        cfg.GithubAppID,
		privateKey:   privateKey,
		appClient:    appClient,
		clients:      make(map[int64]*github.Client),
		transports:   make(map[int64]*ghinstallation.Transport),
		statusMgr:    statusMgr,
		labeler:      autolabeler.NewWithDefaults(),
		cfg:          cfg,
	}

	// Initialize CI fixer agent if enabled
	if cfg.EnableCIFixer {
		if cfg.GCPProjectID == "" {
			log.Fatalf("GCP_PROJECT_ID is required when ENABLE_CI_FIXER=true")
		}

		agent, err := cifixer.NewAgent(ctx, cifixer.AgentConfig{
			GCPProjectID: cfg.GCPProjectID,
			GCPRegion:    cfg.GCPRegion,
			Model:        cfg.ClaudeModel,
		})
		if err != nil {
			log.Fatalf("failed to create CI fixer agent: %v", err)
		}
		reconciler.ciFixerAgent = agent

		log.Infof("CI fixer agent enabled with model %s, max turns %d", cfg.ClaudeModel, cfg.MaxTurns)
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
	statusMgr  *statusmanager.StatusManager[ReconcilerDetails]
	labeler    *autolabeler.Labeler
	cfg        envConfig

	// CI fixer components (nil if disabled)
	ciFixerAgent claudeexecutor.Interface[*cifixer.CIContext, *cifixer.CIFixResult]

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

// getTransportForRepo returns the ghinstallation transport for a repo.
func (r *PRReconciler) getTransportForRepo(ctx context.Context, owner, repo string) (*ghinstallation.Transport, error) {
	// Ensure the client exists (this populates the transport cache)
	if _, err := r.getClientForRepo(ctx, owner, repo); err != nil {
		return nil, err
	}

	installation, _, err := r.appClient.Apps.FindRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("finding installation: %w", err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transports[installation.GetID()], nil
}

// prURLRegex matches GitHub PR URLs like https://github.com/owner/repo/pull/123
var prURLRegex = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/pull/(\d+)$`)

// ciState represents the evaluated state of CI checks
type ciState int

const (
	ciStateNoChecks ciState = iota // No checks exist yet (workflow not started)
	ciStatePending                 // Checks are running
	ciStatePassing                 // All checks passed
	ciStateFailing                 // One or more checks failed
)

// ciStatusResult holds the result of CI status evaluation
type ciStatusResult struct {
	State     ciState
	Reasoning string
}

// evaluateCIStatus determines the CI state based on check counts.
// This is extracted to enable unit testing of the CI evaluation logic.
func evaluateCIStatus(pending, passed, failed, previousTurn int) ciStatusResult {
	// No checks at all - workflow hasn't started yet
	if pending == 0 && passed == 0 && failed == 0 {
		return ciStatusResult{
			State:     ciStateNoChecks,
			Reasoning: "Waiting for CI checks to start",
		}
	}

	// Checks still running
	if pending > 0 {
		return ciStatusResult{
			State:     ciStatePending,
			Reasoning: fmt.Sprintf("Waiting for %d CI checks to complete", pending),
		}
	}

	// All checks completed and none failed
	if failed == 0 {
		if previousTurn > 0 {
			attemptWord := "attempt"
			if previousTurn > 1 {
				attemptWord = "attempts"
			}
			return ciStatusResult{
				State:     ciStatePassing,
				Reasoning: fmt.Sprintf("CI passed after %d fix %s", previousTurn, attemptWord),
			}
		}
		return ciStatusResult{
			State:     ciStatePassing,
			Reasoning: "CI is passing",
		}
	}

	// Some checks failed
	return ciStatusResult{
		State:     ciStateFailing,
		Reasoning: fmt.Sprintf("%d CI checks failed", failed),
	}
}

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

	// Get previous turn count from existing state (for CI fixer continuity)
	previousTurn := 0
	if existingStatus != nil {
		previousTurn = existingStatus.Details.CIFixTurns
	}

	// Determine if we should skip this reconciliation
	// For auto-labeler: skip if we've already processed this SHA
	// For CI fixer: only skip if CI is passing, max turns reached, or needs human
	skipAutoLabeler := existingStatus != nil && existingStatus.ObservedGeneration == sha
	skipCIFixer := existingStatus != nil && existingStatus.ObservedGeneration == sha &&
		(existingStatus.Details.CIFixSuccess ||
			existingStatus.Details.CIFixNeedsHuman ||
			existingStatus.Details.CIFixTurns >= r.cfg.MaxTurns)

	if skipAutoLabeler && skipCIFixer {
		log.Infof("already processed SHA %s, skipping", sha[:8])
		return &workqueue.ProcessResponse{}, nil
	}

	// Set in-progress status
	if err := session.SetActualState(ctx, "Analyzing PR...", &statusmanager.Status[ReconcilerDetails]{
		Status: "in_progress",
	}); err != nil {
		log.Warnf("failed to set in-progress status: %v", err)
	}

	details := ReconcilerDetails{}

	// Run auto-labeler (only if not already processed for this SHA)
	if !skipAutoLabeler {
		if err := r.runAutoLabeler(ctx, gh, owner, repo, number, pr, &details); err != nil {
			log.Errorf("auto-labeler failed: %v", err)
			// Continue to CI fixer even if labeler fails
		}
	} else {
		// Preserve previous labeler results
		details.LabelsApplied = existingStatus.Details.LabelsApplied
		details.FilesAnalyzed = existingStatus.Details.FilesAnalyzed
		details.TotalChanges = existingStatus.Details.TotalChanges
	}

	// Run CI fixer if enabled and the label is present
	if r.ciFixerAgent != nil && hasLabel(pr.Labels, r.cfg.CIFixerLabel) && !skipCIFixer {
		if err := r.runCIFixer(ctx, gh, owner, repo, number, pr, &details, previousTurn); err != nil {
			log.Errorf("CI fixer failed: %v", err)
		}
	} else if existingStatus != nil {
		// Preserve previous CI fixer results if we're skipping
		details.CIFixAttempted = existingStatus.Details.CIFixAttempted
		details.CIFixSuccess = existingStatus.Details.CIFixSuccess
		details.CIFixFiles = existingStatus.Details.CIFixFiles
		details.CIFixReasoning = existingStatus.Details.CIFixReasoning
		details.CIFixTurns = existingStatus.Details.CIFixTurns
		details.CIFixNeedsHuman = existingStatus.Details.CIFixNeedsHuman
		details.CIFixCommit = existingStatus.Details.CIFixCommit
	}

	// Determine conclusion and status
	status := "completed"
	conclusion := "success"
	summary := "Reconciliation complete"
	if details.CIFixPending {
		status = "in_progress"
		conclusion = ""
		summary = "Waiting for CI checks to complete"
	} else if details.CIFixAttempted && !details.CIFixSuccess && !details.CIFixNeedsHuman {
		conclusion = "failure"
		summary = "CI fix attempted but failed"
	} else if details.CIFixNeedsHuman {
		conclusion = "action_required"
		summary = "Human intervention requested"
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

	log.Infof("reconciliation complete: labels=%v ci_fix=%v",
		details.LabelsApplied, details.CIFixSuccess)

	// If CI is pending, requeue to check again in 60 seconds
	if details.CIFixPending {
		log.Infof("CI pending, requeueing to check again in 60 seconds")
		return &workqueue.ProcessResponse{RequeueAfterSeconds: 60}, nil
	}

	return &workqueue.ProcessResponse{}, nil
}

func (r *PRReconciler) runAutoLabeler(ctx context.Context, gh *github.Client, owner, repo string, number int, pr *github.PullRequest, details *ReconcilerDetails) error {
	log := clog.FromContext(ctx)

	// Get changed files
	files, _, err := gh.PullRequests.ListFiles(ctx, owner, repo, number, &github.ListOptions{PerPage: 100})
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}

	// Calculate labels using the autolabeler package
	result := r.labeler.CalculateLabels(files)

	// Filter to only labels we need to add
	newLabels := autolabeler.FilterNewLabels(result.Labels, pr.Labels)

	details.LabelsApplied = result.Labels
	details.FilesAnalyzed = result.FilesAnalyzed
	details.TotalChanges = result.TotalChanges

	// Apply new labels if any
	if len(newLabels) > 0 {
		log.Infof("adding labels: %v", newLabels)
		_, _, err = gh.Issues.AddLabelsToIssue(ctx, owner, repo, number, newLabels)
		if err != nil {
			return fmt.Errorf("adding labels: %w", err)
		}
	} else {
		log.Infof("no new labels to add (calculated: %v)", result.Labels)
	}

	return nil
}

func (r *PRReconciler) runCIFixer(ctx context.Context, gh *github.Client, owner, repo string, number int, pr *github.PullRequest, details *ReconcilerDetails, previousTurn int) error {
	log := clog.FromContext(ctx)

	details.CIFixAttempted = true

	// Get check runs for the PR head
	sha := pr.GetHead().GetSHA()
	checks, _, err := gh.Checks.ListCheckRunsForRef(ctx, owner, repo, sha, nil)
	if err != nil {
		return fmt.Errorf("listing check runs: %w", err)
	}

	// Count check states to determine if we should act
	var pending, failed, passed int
	var failures []cifixer.CheckFailure

	for _, check := range checks.CheckRuns {
		// Skip our own check run
		if check.GetName() == reconcilerIdentity {
			continue
		}

		status := check.GetStatus()
		conclusion := check.GetConclusion()

		if status != "completed" {
			pending++
		} else if conclusion == "success" || conclusion == "skipped" || conclusion == "neutral" {
			passed++
		} else {
			failed++
			// Collect failure details
			logs := r.getCheckLogs(ctx, gh, owner, repo, check.GetID())
			failures = append(failures, cifixer.CheckFailure{
				Name:       check.GetName(),
				Conclusion: conclusion,
				Logs:       logs,
			})
		}
	}

	log.Infof("CI status: pending=%d passed=%d failed=%d", pending, passed, failed)

	// Evaluate CI status and update details accordingly
	status := evaluateCIStatus(pending, passed, failed, previousTurn)
	switch status.State {
	case ciStateNoChecks:
		log.Infof("No CI checks found yet, treating as pending")
		details.CIFixPending = true
		details.CIFixReasoning = status.Reasoning
		details.CIFixTurns = previousTurn
		return nil
	case ciStatePending:
		log.Infof("CI still pending (%d checks), skipping CI fixer", pending)
		details.CIFixPending = true
		details.CIFixReasoning = status.Reasoning
		details.CIFixTurns = previousTurn
		return nil
	case ciStatePassing:
		log.Infof("All %d checks passing", passed)
		details.CIFixSuccess = true
		details.CIFixTurns = previousTurn
		details.CIFixReasoning = status.Reasoning
		return nil
	case ciStateFailing:
		// Continue to attempt fix
	}

	// Calculate current turn
	currentTurn := previousTurn + 1
	if currentTurn > r.cfg.MaxTurns {
		log.Warnf("Max turns (%d) reached without fixing CI", r.cfg.MaxTurns)
		details.CIFixTurns = previousTurn
		details.CIFixReasoning = fmt.Sprintf("Max turns (%d) reached without fixing CI", r.cfg.MaxTurns)
		return nil
	}

	log.Infof("Running CI fixer turn %d/%d with %d failing checks: %v",
		currentTurn, r.cfg.MaxTurns, len(failures), getFailureNames(failures))

	// Get changed files for context
	prFiles, _, err := gh.PullRequests.ListFiles(ctx, owner, repo, number, &github.ListOptions{PerPage: 100})
	if err != nil {
		return fmt.Errorf("listing PR files: %w", err)
	}
	changedFiles := make([]string, 0, len(prFiles))
	for _, f := range prFiles {
		changedFiles = append(changedFiles, f.GetFilename())
	}

	// Get the installation transport for git operations
	transport, err := r.getTransportForRepo(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("getting transport: %w", err)
	}

	// Create GitManager with token source
	tokenSource := cifixer.NewGitHubAppTokenSource(func() (string, error) {
		return transport.Token(ctx)
	})

	gitMgr, err := cifixer.NewGitManager(tokenSource, reconcilerIdentity)
	if err != nil {
		return fmt.Errorf("creating git manager: %w", err)
	}

	// Clone the PR branch
	branch := pr.GetHead().GetRef()
	clone, err := gitMgr.ClonePRBranch(ctx, owner, repo, branch)
	if err != nil {
		return fmt.Errorf("cloning PR branch: %w", err)
	}
	defer func() {
		if err := clone.Close(); err != nil {
			log.Warnf("failed to close clone: %v", err)
		}
	}()

	// Create real filesystem for the clone
	fs := cifixer.NewRealFileSystem(clone.Dir())

	// Run a single turn of the agent
	ciCtx := &cifixer.CIContext{
		Owner:        owner,
		Repo:         repo,
		PRNumber:     number,
		Branch:       branch,
		Turn:         currentTurn,
		MaxTurns:     r.cfg.MaxTurns,
		Failures:     failures,
		ChangedFiles: changedFiles,
	}

	result, err := cifixer.ExecuteWithFS(ctx, r.ciFixerAgent, fs, ciCtx)
	if err != nil {
		details.CIFixReasoning = fmt.Sprintf("Agent execution failed: %v", err)
		details.CIFixTurns = currentTurn
		return fmt.Errorf("agent execution: %w", err)
	}

	details.CIFixTurns = currentTurn

	if result.NeedsHuman {
		details.CIFixNeedsHuman = true
		details.CIFixReasoning = result.Reasoning
		log.Warnf("agent requests human intervention: %s", result.Reasoning)
		return nil
	}

	if !result.Success || len(result.FilesChanged) == 0 {
		details.CIFixReasoning = result.Reasoning
		log.Warnf("agent made no changes on turn %d: %s", currentTurn, result.Reasoning)
		return nil
	}

	// Commit and push the fix
	details.CIFixFiles = result.FilesChanged
	commitMsg := result.CommitMessage
	if commitMsg == "" {
		commitMsg = "fix: automated CI fix"
	}

	log.Infof("committing fix: %s (files: %v)", commitMsg, result.FilesChanged)

	if err := clone.CommitAndPush(ctx, commitMsg); err != nil {
		details.CIFixReasoning = fmt.Sprintf("Failed to push fix: %v", err)
		return fmt.Errorf("commit and push: %w", err)
	}

	details.CIFixCommit = clone.SHA()
	details.CIFixReasoning = result.Reasoning
	log.Infof("pushed commit %s - returning immediately (next reconcile will check CI)", details.CIFixCommit[:8])

	// Return immediately - don't wait for CI!
	// The workqueue will trigger a new reconciliation when GitHub workflow
	// events fire on the new commit.
	return nil
}

func (r *PRReconciler) getCheckLogs(ctx context.Context, gh *github.Client, owner, repo string, checkRunID int64) string {
	// GitHub doesn't provide a direct API to get check run logs.
	// We can get annotations which often contain error details.
	annotations, _, err := gh.Checks.ListCheckRunAnnotations(ctx, owner, repo, checkRunID, nil)
	if err != nil {
		return fmt.Sprintf("(failed to get annotations: %v)", err)
	}

	var sb strings.Builder
	for _, a := range annotations {
		fmt.Fprintf(&sb, "%s:%d: %s - %s\n",
			a.GetPath(), a.GetStartLine(), a.GetAnnotationLevel(), a.GetMessage())
	}

	// Also try to get the check run output
	checkRun, _, err := gh.Checks.GetCheckRun(ctx, owner, repo, checkRunID)
	if err == nil && checkRun.Output != nil {
		if summary := checkRun.Output.GetSummary(); summary != "" {
			sb.WriteString("\n--- Summary ---\n")
			sb.WriteString(summary)
		}
		if text := checkRun.Output.GetText(); text != "" {
			sb.WriteString("\n--- Details ---\n")
			// Truncate very long output
			if len(text) > 5000 {
				text = text[:5000] + "\n... (truncated)"
			}
			sb.WriteString(text)
		}
	}

	if sb.Len() == 0 {
		return "(no logs available)"
	}

	return sb.String()
}

func getFailureNames(failures []cifixer.CheckFailure) []string {
	names := make([]string, len(failures))
	for i, f := range failures {
		names[i] = f.Name
	}
	return names
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