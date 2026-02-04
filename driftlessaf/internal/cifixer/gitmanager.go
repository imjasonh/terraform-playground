package cifixer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"golang.org/x/oauth2"
)

// GitManager manages git operations for CI fixing.
// It handles cloning PR branches, making changes, and pushing commits.
type GitManager struct {
	tokenSource oauth2.TokenSource
	identity    string
}

// NewGitManager creates a new GitManager.
// tokenSource provides GitHub authentication.
// identity is used as the commit author name.
func NewGitManager(tokenSource oauth2.TokenSource, identity string) (*GitManager, error) {
	if tokenSource == nil {
		return nil, errors.New("token source cannot be nil")
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, errors.New("identity cannot be empty")
	}
	return &GitManager{
		tokenSource: tokenSource,
		identity:    identity,
	}, nil
}

// PRClone represents a cloned PR branch ready for modifications.
type PRClone struct {
	manager *GitManager
	dir     string
	repo    *git.Repository
	branch  string
	sha     string
}

// ClonePRBranch clones a repository and checks out the PR's head branch.
func (m *GitManager) ClonePRBranch(ctx context.Context, owner, repo, branch string) (*PRClone, error) {
	log := clog.FromContext(ctx)

	// Create temp directory
	dir, err := os.MkdirTemp("", "cifixer-clone-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	// Get auth
	auth, err := m.authForRemote()
	if err != nil {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			log.Warnf("failed to clean up temp dir after auth error: %v", removeErr)
		}
		return nil, fmt.Errorf("getting auth: %w", err)
	}

	// Clone the repository
	repoURL := fmt.Sprintf("https://github.com/%s/%s", owner, repo)
	log.Infof("Cloning %s branch %s into %s", repoURL, branch, dir)

	gitRepo, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL:           repoURL,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Auth:          auth,
		Depth:         50, // Shallow clone for speed
	})
	if err != nil {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			log.Warnf("failed to clean up temp dir after clone error: %v", removeErr)
		}
		return nil, fmt.Errorf("cloning repository: %w", err)
	}

	// Get the current HEAD SHA
	head, err := gitRepo.Head()
	if err != nil {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			log.Warnf("failed to clean up temp dir after HEAD error: %v", removeErr)
		}
		return nil, fmt.Errorf("getting HEAD: %w", err)
	}

	log.Infof("Cloned successfully at %s", head.Hash().String()[:8])

	return &PRClone{
		manager: m,
		dir:     dir,
		repo:    gitRepo,
		branch:  branch,
		sha:     head.Hash().String(),
	}, nil
}

// Dir returns the path to the cloned repository.
func (c *PRClone) Dir() string {
	return c.dir
}

// SHA returns the current HEAD commit SHA.
func (c *PRClone) SHA() string {
	return c.sha
}

// CommitAndPush stages all changes, commits them, and pushes to the remote.
func (c *PRClone) CommitAndPush(ctx context.Context, message string) error {
	log := clog.FromContext(ctx)

	worktree, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	// Stage all changes
	if err := worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("staging changes: %w", err)
	}

	// Check if there are any changes to commit
	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	if status.IsClean() {
		log.Infof("No changes to commit")
		return nil
	}

	// Build author email
	email := c.manager.identity
	if !strings.Contains(email, "@") {
		email = fmt.Sprintf("%s@users.noreply.github.com", email)
	}

	// Commit
	commit, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.manager.identity,
			Email: email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	log.Infof("Committed %s: %s", commit.String()[:8], message)

	// Get auth for push
	auth, err := c.manager.authForRemote()
	if err != nil {
		return fmt.Errorf("getting auth for push: %w", err)
	}

	// Push to remote
	refSpec := gitconfig.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", c.branch, c.branch))
	log.Infof("Pushing to %s", refSpec)

	if err := c.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       auth,
		RefSpecs:   []gitconfig.RefSpec{refSpec},
	}); err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			log.Infof("Branch already up to date")
			return nil
		}
		return fmt.Errorf("pushing: %w", err)
	}

	// Update our SHA
	head, err := c.repo.Head()
	if err == nil {
		c.sha = head.Hash().String()
	}

	log.Infof("Push successful")
	return nil
}

// Close cleans up the cloned repository.
func (c *PRClone) Close() error {
	if c.dir != "" {
		return os.RemoveAll(c.dir)
	}
	return nil
}

func (m *GitManager) authForRemote() (*githttp.BasicAuth, error) {
	token, err := m.tokenSource.Token()
	if err != nil {
		return nil, err
	}

	return &githttp.BasicAuth{
		Username: "x-access-token",
		Password: token.AccessToken,
	}, nil
}

// GitHubAppTokenSource implements oauth2.TokenSource using a GitHub App installation.
type GitHubAppTokenSource struct {
	getToken func() (string, error)
}

// NewGitHubAppTokenSource creates a token source that uses a function to get tokens.
// This allows integration with ghinstallation transports.
func NewGitHubAppTokenSource(getToken func() (string, error)) *GitHubAppTokenSource {
	return &GitHubAppTokenSource{getToken: getToken}
}

// Token returns an OAuth2 token.
func (s *GitHubAppTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.getToken()
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{
		AccessToken: token,
		TokenType:   "Bearer",
	}, nil
}