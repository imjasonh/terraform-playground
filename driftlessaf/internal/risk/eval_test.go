package risk

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"chainguard.dev/driftlessaf/agents/executor/claudeexecutor"
	"chainguard.dev/driftlessaf/agents/judge"
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

// TestRiskAssessmentEvals runs the risk assessment agent against test cases and evaluates results.
// This test is skipped by default and only runs when -run-evals flag is set.
func TestRiskAssessmentEvals(t *testing.T) {
	if !*runEvals {
		t.Skip("Skipping LLM-based evals (use -run-evals to enable)")
	}

	ctx := context.Background()
	log := clog.FromContext(ctx)

	// Create the risk assessment agent (will auto-detect project/region if not provided)
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
	Name            string
	Success         bool
	Score           float64
	Reasoning       string
	LevelMatch      bool
	ActualLevel     string
	ExpectedLevel   string
	AgentAssessment *RiskAssessment
	Error           error
}

func runSingleEval(t *testing.T, ctx context.Context, agent claudeexecutor.Interface[*PRContext, *RiskAssessment], judgeInstance judge.Interface, tc EvalTestCase) evalResult {
	result := evalResult{
		Name:          tc.Name,
		ExpectedLevel: string(tc.ExpectedLevel),
	}

	// Execute agent - no tools needed for risk assessment
	assessment, err := agent.Execute(ctx, tc.PRContext, nil)
	if err != nil {
		result.Error = err
		t.Errorf("agent execution failed: %v", err)
		return result
	}

	result.AgentAssessment = assessment
	result.ActualLevel = assessment.RiskLevel

	// Check if risk level matches expected
	result.LevelMatch = strings.EqualFold(assessment.RiskLevel, string(tc.ExpectedLevel))

	if !result.LevelMatch && *verbose {
		t.Logf("Level mismatch: got %s, want %s", assessment.RiskLevel, tc.ExpectedLevel)
		t.Logf("Reasoning: %s", assessment.Reasoning)
	}

	// Use judge for quality evaluation
	actualAnswer := formatAgentAssessment(assessment)

	judgement, err := judgeInstance.Judge(ctx, &judge.Request{
		Mode:         judge.StandaloneMode,
		ActualAnswer: actualAnswer,
		Criterion:    tc.Criterion,
	})

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

func formatAgentAssessment(assessment *RiskAssessment) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Risk Level: %s\n", assessment.RiskLevel))
	sb.WriteString(fmt.Sprintf("Confidence: %.2f\n", assessment.Confidence))
	sb.WriteString(fmt.Sprintf("\nReasoning:\n%s\n", assessment.Reasoning))

	if len(assessment.RiskFactors) > 0 {
		sb.WriteString("\nRisk Factors:\n")
		for _, factor := range assessment.RiskFactors {
			sb.WriteString(fmt.Sprintf("- %s\n", factor))
		}
	}

	if len(assessment.RiskyFiles) > 0 {
		sb.WriteString("\nRisky Files:\n")
		for _, file := range assessment.RiskyFiles {
			sb.WriteString(fmt.Sprintf("- %s\n", file))
		}
	}

	return sb.String()
}

func formatExpectedAssessment(tc EvalTestCase) string {
	return fmt.Sprintf("Expected Risk Level: %s\n\nDescription: %s", tc.ExpectedLevel, tc.Description)
}

func printEvalSummary(t *testing.T, results []evalResult) {
	t.Helper()

	totalTests := len(results)
	passed := 0
	levelMatches := 0

	var totalScore float64

	t.Log("\n" + strings.Repeat("=", 80))
	t.Log("EVALUATION SUMMARY")
	t.Log(strings.Repeat("=", 80))

	for _, r := range results {
		if r.Success {
			passed++
		}
		if r.LevelMatch {
			levelMatches++
		}
		totalScore += r.Score

		status := "❌ FAIL"
		if r.Success {
			status = "✅ PASS"
		}

		levelStatus := "❌"
		if r.LevelMatch {
			levelStatus = "✅"
		}

		t.Logf("%s | Score: %.2f | %s Level: %s (expected %s) | %s",
			status, r.Score, levelStatus, r.ActualLevel, r.ExpectedLevel, r.Name)

		if r.Error != nil {
			t.Logf("  Error: %v", r.Error)
		}
	}

	avgScore := totalScore / float64(totalTests)
	passRate := float64(passed) / float64(totalTests) * 100
	levelMatchRate := float64(levelMatches) / float64(totalTests) * 100

	t.Log(strings.Repeat("=", 80))
	t.Logf("Tests Passed: %d/%d (%.1f%%)", passed, totalTests, passRate)
	t.Logf("Level Matches: %d/%d (%.1f%%)", levelMatches, totalTests, levelMatchRate)
	t.Logf("Average Score: %.2f", avgScore)
	t.Log(strings.Repeat("=", 80))

	// Write results to file for tracking
	if err := writeResultsToFile(results); err != nil {
		t.Logf("Warning: failed to write results to file: %v", err)
	}
}

func writeResultsToFile(results []evalResult) error {
	f, err := os.Create("eval_results.log")
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}
