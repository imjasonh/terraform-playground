package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-infra-common/pkg/httpmetrics"
	"github.com/chainguard-dev/terraform-infra-common/pkg/workqueue"
	"github.com/google/go-github/v68/github"
	"github.com/sethvargo/go-envconfig"
	"google.golang.org/grpc"
)

type envConfig struct {
	Port            int    `env:"PORT, default=8080"`
	GithubAppID     int64  `env:"GITHUB_APP_ID, required"`
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

	reconciler := &PRReconciler{
		appID:      cfg.GithubAppID,
		privateKey: privateKey,
		appClient:  appClient,
		clients:    make(map[int64]*github.Client),
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	workqueue.RegisterWorkqueueServiceServer(grpcServer, reconciler)

	httpmetrics.ServeMetrics()
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
	log.Debugf("found installation %d for %s/%s", installationID, owner, repo)

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

	// Get a client for this repo's installation
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

	log.Infof("processing PR",
		"owner", owner,
		"repo", repo,
		"number", number,
		"title", pr.GetTitle(),
		"state", pr.GetState(),
		"author", pr.GetUser().GetLogin(),
		"head", pr.GetHead().GetRef(),
		"base", pr.GetBase().GetRef(),
		"mergeable", pr.GetMergeable(),
		"additions", pr.GetAdditions(),
		"deletions", pr.GetDeletions(),
		"changed_files", pr.GetChangedFiles(),
	)

	var labelNames []string
	for _, label := range pr.Labels {
		labelNames = append(labelNames, label.GetName())
	}
	log.Infof("PR labels", "labels", labelNames)

	reviews, _, err := gh.PullRequests.ListReviews(ctx, owner, repo, number, nil)
	if err != nil {
		log.Warnf("failed to fetch reviews: %v", err)
	} else {
		for _, review := range reviews {
			log.Infof("PR review",
				"reviewer", review.GetUser().GetLogin(),
				"state", review.GetState(),
				"submitted_at", review.GetSubmittedAt(),
			)
		}
	}

	combinedStatus, _, err := gh.Repositories.GetCombinedStatus(ctx, owner, repo, pr.GetHead().GetSHA(), nil)
	if err != nil {
		log.Warnf("failed to fetch combined status: %v", err)
	} else {
		log.Infof("PR combined status",
			"state", combinedStatus.GetState(),
			"total_count", combinedStatus.GetTotalCount(),
		)
		for _, status := range combinedStatus.Statuses {
			log.Infof("PR status",
				"context", status.GetContext(),
				"state", status.GetState(),
				"description", status.GetDescription(),
			)
		}
	}

	checkRuns, _, err := gh.Checks.ListCheckRunsForRef(ctx, owner, repo, pr.GetHead().GetSHA(), nil)
	if err != nil {
		log.Warnf("failed to fetch check runs: %v", err)
	} else {
		for _, check := range checkRuns.CheckRuns {
			log.Infof("PR check run",
				"name", check.GetName(),
				"status", check.GetStatus(),
				"conclusion", check.GetConclusion(),
			)
		}
	}

	files, _, err := gh.PullRequests.ListFiles(ctx, owner, repo, number, nil)
	if err != nil {
		log.Warnf("failed to fetch changed files: %v", err)
	} else {
		for _, file := range files {
			log.Infof("PR file change",
				"filename", file.GetFilename(),
				"status", file.GetStatus(),
				"additions", file.GetAdditions(),
				"deletions", file.GetDeletions(),
			)
		}
	}

	return &workqueue.ProcessResponse{}, nil
}

func (r *PRReconciler) GetKeyState(ctx context.Context, req *workqueue.GetKeyStateRequest) (*workqueue.KeyState, error) {
	return &workqueue.KeyState{}, nil
}
