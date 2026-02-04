package cifixer

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"chainguard.dev/driftlessaf/agents/executor/claudeexecutor"
	"chainguard.dev/driftlessaf/agents/submitresult"
	"cloud.google.com/go/compute/metadata"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/vertex"
)

// AgentConfig holds the configuration for creating a CI fixer agent.
type AgentConfig struct {
	// GCPProjectID is the Google Cloud project ID for Vertex AI.
	GCPProjectID string

	// GCPRegion is the Google Cloud region for Vertex AI (e.g., "us-central1").
	GCPRegion string

	// Model is the Claude model to use (e.g., "claude-sonnet-4-20250514").
	Model string

	// MaxTokens is the maximum number of tokens in the response.
	MaxTokens int64

	// Temperature controls randomness (0.0-1.0). Lower is more deterministic.
	Temperature float64
}

// DefaultConfig returns a default agent configuration.
func DefaultConfig() AgentConfig {
	return AgentConfig{
		Model:       "claude-sonnet-4@20250514", // Vertex AI format uses @ instead of -
		MaxTokens:   16384,
		Temperature: 0.1,
	}
}

// detectGCPProjectID attempts to detect the GCP project ID from:
// 1. GCE metadata service (if running on GCP)
// 2. gcloud CLI configuration
func detectGCPProjectID() string {
	// Try metadata service first (fast on GCP, quick timeout elsewhere)
	if metadata.OnGCE() {
		if project, err := metadata.ProjectIDWithContext(context.Background()); err == nil && project != "" {
			return project
		}
	}

	// Try gcloud CLI
	cmd := exec.Command("gcloud", "config", "get-value", "project")
	out, err := cmd.Output()
	if err == nil {
		project := strings.TrimSpace(string(out))
		if project != "" && project != "(unset)" {
			return project
		}
	}

	return ""
}

// detectGCPRegion attempts to detect the GCP region from:
// 1. GCE metadata service (if running on GCP)
// 2. gcloud CLI configuration
// Falls back to "us-central1" if nothing is found.
func detectGCPRegion() string {
	// Try metadata service first
	if metadata.OnGCE() {
		// Zone is like "us-central1-a", we want "us-central1"
		if zone, err := metadata.ZoneWithContext(context.Background()); err == nil && zone != "" {
			parts := strings.Split(zone, "-")
			if len(parts) >= 2 {
				return strings.Join(parts[:len(parts)-1], "-")
			}
		}
	}

	// Try gcloud CLI for compute region
	cmd := exec.Command("gcloud", "config", "get-value", "compute/region")
	out, err := cmd.Output()
	if err == nil {
		region := strings.TrimSpace(string(out))
		if region != "" && region != "(unset)" {
			return region
		}
	}

	// Default to us-east5 which typically has Claude models available
	return "us-east5"
}

// NewAgent creates a new CI fixer agent.
// If GCPProjectID or GCPRegion are not provided, they will be detected from:
// 1. GCE metadata service (if running on GCP)
// 2. gcloud CLI configuration
// 3. Default to "us-central1" for region (project ID has no default)
func NewAgent(ctx context.Context, cfg AgentConfig) (claudeexecutor.Interface[*CIContext, *CIFixResult], error) {
	// Detect GCP project ID if not provided
	if cfg.GCPProjectID == "" {
		cfg.GCPProjectID = detectGCPProjectID()
		if cfg.GCPProjectID == "" {
			return nil, fmt.Errorf("GCPProjectID is required (could not detect from metadata or gcloud)")
		}
	}

	// Detect GCP region if not provided
	if cfg.GCPRegion == "" {
		cfg.GCPRegion = detectGCPRegion()
	}

	// Create Anthropic client via Vertex AI
	client := anthropic.NewClient(
		vertex.WithGoogleAuth(ctx, cfg.GCPRegion, cfg.GCPProjectID),
	)

	// Apply defaults
	if cfg.Model == "" {
		cfg.Model = DefaultConfig().Model
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultConfig().MaxTokens
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = DefaultConfig().Temperature
	}

	return claudeexecutor.New[*CIContext, *CIFixResult](
		client,
		UserPrompt,
		claudeexecutor.WithModel[*CIContext, *CIFixResult](cfg.Model),
		claudeexecutor.WithMaxTokens[*CIContext, *CIFixResult](cfg.MaxTokens),
		claudeexecutor.WithTemperature[*CIContext, *CIFixResult](cfg.Temperature),
		claudeexecutor.WithSystemInstructions[*CIContext, *CIFixResult](SystemInstructions),
		claudeexecutor.WithSubmitResultProvider[*CIContext, *CIFixResult](
			submitresult.ClaudeToolForResponse[*CIFixResult],
		),
	)
}

// NewAgentWithClient creates a new CI fixer agent with a provided Anthropic client.
// This is useful for testing or when using a non-Vertex AI client.
func NewAgentWithClient(client anthropic.Client, cfg AgentConfig) (claudeexecutor.Interface[*CIContext, *CIFixResult], error) {
	// Apply defaults
	if cfg.Model == "" {
		cfg.Model = DefaultConfig().Model
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultConfig().MaxTokens
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = DefaultConfig().Temperature
	}

	return claudeexecutor.New[*CIContext, *CIFixResult](
		client,
		UserPrompt,
		claudeexecutor.WithModel[*CIContext, *CIFixResult](cfg.Model),
		claudeexecutor.WithMaxTokens[*CIContext, *CIFixResult](cfg.MaxTokens),
		claudeexecutor.WithTemperature[*CIContext, *CIFixResult](cfg.Temperature),
		claudeexecutor.WithSystemInstructions[*CIContext, *CIFixResult](SystemInstructions),
		claudeexecutor.WithSubmitResultProvider[*CIContext, *CIFixResult](
			submitresult.ClaudeToolForResponse[*CIFixResult],
		),
	)
}

// ExecuteWithFS executes the agent with a given filesystem.
// This is the main entry point for running the CI fixer.
func ExecuteWithFS(ctx context.Context, agent claudeexecutor.Interface[*CIContext, *CIFixResult], fs FileSystem, ciCtx *CIContext) (*CIFixResult, error) {
	tools := CreateTools(fs)
	return agent.Execute(ctx, ciCtx, tools)
}