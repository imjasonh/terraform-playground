package cifixer

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"chainguard.dev/driftlessaf/agents/evals"
	"chainguard.dev/driftlessaf/agents/executor/claudeexecutor"
	"chainguard.dev/driftlessaf/agents/judge"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/chainguard-dev/clog"
)

var (
	runEvals     = flag.Bool("run-evals", false, "Run LLM-based evaluations (requires credentials)")
	gcpProjectID = flag.String("gcp-project", os.Getenv("GCP_PROJECT_ID"), "GCP project ID for Vertex AI")
	gcpRegion    = flag.String("gcp-region", os.Getenv("GCP_REGION"), "GCP region for Vertex AI")
	evalModel    = flag.String("eval-model", "claude-sonnet-4@20250514", "Model to use for evaluation (Vertex AI format)")
	judgeModel   = flag.String("judge-model", "claude-sonnet-4@20250514", "Model to use for judging (Vertex AI format)")
	verbose      = flag.Bool("verbose", false, "Print detailed output")
)

// TestCIFixerEvals runs the CI fixer agent against the test cases and evaluates results.
// This test is skipped by default and only runs when -run-evals flag is set.
func TestCIFixerEvals(t *testing.T) {
	if !*runEvals {
		t.Skip("Skipping LLM-based evals (use -run-evals to enable)")
	}

	ctx := context.Background()
	log := clog.FromContext(ctx)

	// Create the CI fixer agent (will auto-detect project/region if not provided)
	agent, err := NewAgent(ctx, AgentConfig{
		GCPProjectID: *gcpProjectID,
		GCPRegion:    *gcpRegion,
		Model:        *evalModel,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}

	// Resolve project/region for judge (use same detection logic)
	projectID := *gcpProjectID
	if projectID == "" {
		projectID = detectGCPProjectID()
		if projectID == "" {
			t.Fatal("could not detect GCP project ID from metadata or gcloud")
		}
	}
	region := *gcpRegion
	if region == "" {
		region = detectGCPRegion()
	}

	log.Infof("Using GCP project=%s region=%s", projectID, region)

	// Create the judge for evaluating results
	judgeInstance, err := judge.NewVertex(ctx, projectID, region, *judgeModel)
	if err != nil {
		t.Fatalf("creating judge: %v", err)
	}

	// Track results for summary
	var results []evalResult

	for _, tc := range EvalTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			result := runSingleEval(t, ctx, agent, judgeInstance, tc)
			results = append(results, result)

			if *verbose {
				log.Infof("Test %s: score=%.2f, success=%v", tc.Name, result.Score, result.Success)
				if result.Reasoning != "" {
					log.Infof("  Reasoning: %s", result.Reasoning)
				}
			}
		})
	}

	// Print summary
	printEvalSummary(t, results)
}

type evalResult struct {
	Name       string
	Success    bool
	Score      float64
	Reasoning  string
	FilesMatch bool
	Error      error
}

func runSingleEval(t *testing.T, ctx context.Context, agent claudeexecutor.Interface[*CIContext, *CIFixResult], judgeInstance judge.Interface, tc EvalTestCase) evalResult {
	result := evalResult{Name: tc.Name}

	// Create mock filesystem with test files
	mockFS := NewMockFileSystem(tc.Files)

	// Build CI context
	ciCtx := &CIContext{
		Owner:    "test",
		Repo:     "test-repo",
		PRNumber: 1,
		Branch:   "fix-ci",
		Turn:     1,
		MaxTurns: 3,
		Failures: []CheckFailure{{
			Name:       "build",
			Conclusion: "failure",
			Logs:       tc.CILogs,
		}},
		ChangedFiles: getFilenames(tc.Files),
	}

	// Execute agent
	agentResult, err := ExecuteWithFS(ctx, agent, mockFS, ciCtx)
	if err != nil {
		result.Error = err
		t.Errorf("agent execution failed: %v", err)
		return result
	}

	// Check if agent made changes
	if !agentResult.Success || len(agentResult.FilesChanged) == 0 {
		result.Error = fmt.Errorf("agent made no changes: %s", agentResult.Reasoning)
		t.Errorf("agent made no changes: %s", agentResult.Reasoning)
		return result
	}

	// Verify files match expected (for deterministic cases)
	result.FilesMatch = checkFilesMatch(mockFS, tc.ExpectedFix)

	if tc.Deterministic && !result.FilesMatch {
		t.Errorf("files don't match expected output")
		for path, expected := range tc.ExpectedFix {
			actual, _ := mockFS.ReadFile(path)
			if actual != expected {
				t.Errorf("file %s mismatch:\ngot:\n%s\nwant:\n%s", path, actual, expected)
			}
		}
	}

	// Use judge for quality evaluation
	actualAnswer := formatAgentResult(mockFS, agentResult)
	expectedAnswer := formatExpectedResult(tc.ExpectedFix)

	var judgement *judge.Judgement
	if tc.Deterministic {
		// Use golden mode for deterministic cases
		judgement, err = judgeInstance.Judge(ctx, &judge.Request{
			Mode:            judge.GoldenMode,
			ReferenceAnswer: expectedAnswer,
			ActualAnswer:    actualAnswer,
			Criterion:       tc.Criterion,
		})
	} else {
		// Use standalone mode for non-deterministic cases
		judgement, err = judgeInstance.Judge(ctx, &judge.Request{
			Mode:         judge.StandaloneMode,
			ActualAnswer: actualAnswer,
			Criterion:    tc.Criterion,
		})
	}

	if err != nil {
		result.Error = fmt.Errorf("judge error: %w", err)
		t.Errorf("judge error: %v", err)
		return result
	}

	result.Score = judgement.Score
	result.Reasoning = judgement.Reasoning
	result.Success = judgement.Score >= 0.7

	if !result.Success {
		t.Errorf("judge score %.2f below threshold 0.7: %s", judgement.Score, judgement.Reasoning)
	}

	return result
}

func checkFilesMatch(fs *MockFileSystem, expected map[string]string) bool {
	for path, expectedContent := range expected {
		actual, err := fs.ReadFile(path)
		if err != nil {
			return false
		}
		if normalizeWhitespaceForEval(actual) != normalizeWhitespaceForEval(expectedContent) {
			return false
		}
	}
	return true
}

// normalizeWhitespaceForEval normalizes whitespace for comparison in evals.
// Note: Similar function exists in cifixer_test.go - this is separate for eval context.
func normalizeWhitespaceForEval(s string) string {
	// Normalize line endings and trailing whitespace for comparison
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatAgentResult(fs *MockFileSystem, result *CIFixResult) string {
	var sb strings.Builder
	sb.WriteString("Agent Result:\n")
	sb.WriteString(fmt.Sprintf("Success: %v\n", result.Success))
	sb.WriteString(fmt.Sprintf("Files Changed: %v\n", result.FilesChanged))
	sb.WriteString(fmt.Sprintf("Commit Message: %s\n", result.CommitMessage))
	sb.WriteString(fmt.Sprintf("Reasoning: %s\n", result.Reasoning))
	sb.WriteString("\nFile Contents:\n")

	for _, path := range result.FilesChanged {
		content, _ := fs.ReadFile(path)
		sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n", path, content))
	}

	return sb.String()
}

func formatExpectedResult(expected map[string]string) string {
	var sb strings.Builder
	sb.WriteString("Expected Result:\n")

	for path, content := range expected {
		sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n", path, content))
	}

	return sb.String()
}

func getFilenames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	return names
}

func printEvalSummary(t *testing.T, results []evalResult) {
	t.Log("\n" + strings.Repeat("=", 60))
	t.Log("EVALUATION SUMMARY")
	t.Log(strings.Repeat("=", 60))

	var totalScore float64
	var passed, failed int

	for _, r := range results {
		status := "✓ PASS"
		if !r.Success {
			status = "✗ FAIL"
			failed++
		} else {
			passed++
		}
		totalScore += r.Score

		t.Logf("%-30s %s (score: %.2f)", r.Name, status, r.Score)
		if r.Error != nil {
			t.Logf("  Error: %v", r.Error)
		}
	}

	t.Log(strings.Repeat("-", 60))
	avgScore := totalScore / float64(len(results))
	t.Logf("Total: %d/%d passed (%.1f%%)", passed, len(results), float64(passed)/float64(len(results))*100)
	t.Logf("Average Score: %.2f", avgScore)

	if avgScore < 0.8 {
		t.Errorf("Average score %.2f is below threshold 0.8", avgScore)
	}
}

// TestCIFixerEvalsOffline runs evaluations without LLM calls for basic validation.
// This tests the harness itself and validates test case structure.
func TestCIFixerEvalsOffline(t *testing.T) {
	for _, tc := range EvalTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			// Validate test case structure
			if tc.Name == "" {
				t.Error("test case missing name")
			}
			if len(tc.Files) == 0 {
				t.Error("test case missing files")
			}
			if tc.CILogs == "" {
				t.Error("test case missing CI logs")
			}
			if len(tc.ExpectedFix) == 0 {
				t.Error("test case missing expected fix")
			}
			if tc.Criterion == "" {
				t.Error("test case missing criterion")
			}

			// Verify expected fix files are a subset of or equal to input files
			for path := range tc.ExpectedFix {
				if _, ok := tc.Files[path]; !ok {
					// It's okay to create new files, but log it
					t.Logf("expected fix creates new file: %s", path)
				}
			}
		})
	}
}

// TestToolBehaviorWithTracing tests that tools work correctly with tracing enabled.
func TestToolBehaviorWithTracing(t *testing.T) {
	ctx := context.Background()

	// Create a namespaced observer for testing
	obs := evals.NewNamespacedObserver(func(name string) *testObserver {
		return &testObserver{t: t, name: name}
	})
	tracer := evals.BuildTracer[*CIFixResult](obs, map[string]evals.ObservableTraceCallback[*CIFixResult]{
		"no-errors": evals.NoErrors[*CIFixResult](),
	})

	// Create a trace
	trace := tracer.NewTrace(ctx, "test prompt")

	// Create mock filesystem
	mockFS := NewMockFileSystem(map[string]string{
		"test.go": "package test\n",
	})

	// Create tools
	tools := CreateTools(mockFS)

	// Simulate tool calls by directly calling handlers
	readTool := tools["read_file"]
	toolUse := createTestToolUseBlock("read_file", map[string]any{"path": "test.go"})

	result := readTool.Handler(ctx, toolUse, trace, nil)

	if errMsg, ok := result["error"].(string); ok {
		t.Errorf("unexpected error: %s", errMsg)
	}

	content, ok := result["content"].(string)
	if !ok || content != "package test\n" {
		t.Errorf("unexpected content: %v", result)
	}
}

type testObserver struct {
	t        *testing.T
	name     string
	failures []string
	total    int64
}

func (o *testObserver) Fail(msg string) {
	o.failures = append(o.failures, msg)
	o.t.Errorf("observer %s failure: %s", o.name, msg)
}

func (o *testObserver) Log(msg string) {
	o.t.Logf("observer %s log: %s", o.name, msg)
}

func (o *testObserver) Grade(score float64, reasoning string) {
	o.t.Logf("observer %s grade: %.2f - %s", o.name, score, reasoning)
}

func (o *testObserver) Increment() {
	o.total++
}

func (o *testObserver) Total() int64 {
	return o.total
}

func createTestToolUseBlock(name string, input map[string]any) anthropic.ToolUseBlock {
	inputBytes, _ := json.Marshal(input)
	return anthropic.ToolUseBlock{
		ID:    "test-id",
		Name:  name,
		Input: inputBytes,
	}
}

// Ensure claudeexecutor is used (for Interface type)
var _ claudeexecutor.Interface[*CIContext, *CIFixResult] = nil
