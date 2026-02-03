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

	log.Infof("processing PR: title=%q state=%s author=%s",
		pr.GetTitle(), pr.GetState(), pr.GetUser().GetLogin())

	var labelNames []string
	for _, label := range pr.Labels {
		labelNames = append(labelNames, label.GetName())
	}
	if len(labelNames) > 0 {
		log.Infof("PR labels: %v", labelNames)
	}

	reviews, _, err := gh.PullRequests.ListReviews(ctx, owner, repo, number, nil)
	if err != nil {
		log.Warnf("failed to fetch reviews: %v", err)
	} else if len(reviews) > 0 {
		for _, review := range reviews {
			log.Infof("review: reviewer=%s state=%s", review.GetUser().GetLogin(), review.GetState())
		}
	}

	combinedStatus, _, err := gh.Repositories.GetCombinedStatus(ctx, owner, repo, pr.GetHead().GetSHA(), nil)
	if err != nil {
		log.Warnf("failed to fetch combined status: %v", err)
	} else {
		log.Infof("combined status: state=%s count=%d", combinedStatus.GetState(), combinedStatus.GetTotalCount())
	}

	checkRuns, _, err := gh.Checks.ListCheckRunsForRef(ctx, owner, repo, pr.GetHead().GetSHA(), nil)
	if err != nil {
		log.Warnf("failed to fetch check runs: %v", err)
	} else if len(checkRuns.CheckRuns) > 0 {
		log.Infof("check runs: count=%d", len(checkRuns.CheckRuns))
	}

	files, _, err := gh.PullRequests.ListFiles(ctx, owner, repo, number, nil)
	if err != nil {
		log.Warnf("failed to fetch changed files: %v", err)
	} else {
		log.Infof("changed files: count=%d", len(files))
	}

	// Fetch the latest 5 comments on the PR
	comments, _, err := gh.Issues.ListComments(ctx, owner, repo, number, &github.IssueListCommentsOptions{
		Sort:        github.Ptr("created"),
		Direction:   github.Ptr("desc"),
		ListOptions: github.ListOptions{PerPage: 5},
	})
	if err != nil {
		log.Warnf("failed to fetch comments: %v", err)
	} else if len(comments) > 0 {
		log.Infof("latest comments: count=%d", len(comments))
		for _, comment := range comments {
			body := comment.GetBody()
			if len(body) > 80 {
				body = body[:80] + "..."
			}
			log.Infof("  comment by %s at %s: %q",
				comment.GetUser().GetLogin(),
				comment.GetCreatedAt().Format("2006-01-02 15:04:05"),
				body)
		}
	}

	return &workqueue.ProcessResponse{}, nil
}

func (r *PRReconciler) GetKeyState(ctx context.Context, req *workqueue.GetKeyStateRequest) (*workqueue.KeyState, error) {
	return &workqueue.KeyState{}, nil
}
