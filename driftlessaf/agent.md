# Building a CI-Fixing Agent with go-driftlessaf

## Overview

This document describes the architecture of `go-driftlessaf` and how we can build an agent that detects failing CI on a PR, instructs an AI model to fix it (including using tool calls), and pushes commits to the branch repeatedly until CI passes or a turn limit is reached.

**Critical principle: Evals before integration.** We must validate that our prompts produce good results through rigorous evaluation before connecting to GitHub. This prevents wasting CI cycles, creating noise in repositories, and building confidence in our agent's behavior.

## Understanding go-driftlessaf

[go-driftlessaf](https://github.com/driftlessaf/go-driftlessaf) is Chainguard's foundational agentic framework for building AI-powered automation and resilient GitHub reconcilers. It provides:

- **Production-ready AI executors** for Anthropic Claude and Google Gemini
- **Kubernetes-pattern reconciliation** adapted for GitHub
- **GCS-backed state persistence** with retry logic
- **Comprehensive evaluation framework** for testing agent quality

### Key Components

#### 1. Executor (`agents/executor/`)

The executor is the core engine for running agentic conversations. For Claude, it's `claudeexecutor.Interface[Request, Response]`:

```go
type Interface[Request promptbuilder.Bindable, Response any] interface {
    Execute(ctx context.Context, request Request,
            tools map[string]ToolMetadata[Response],
            seedToolCalls ...anthropic.ToolUseBlock) (Response, error)
}
```

**Key features:**
- Streams Claude API responses
- Handles iterative tool execution loops
- Records token metrics and traces
- Supports extended thinking with configurable budget
- Continues until Claude returns a final text response or submits a result

The executor maintains a conversation loop:
1. Bind request to prompt template
2. Execute optional seed tool calls upfront
3. Stream Claude response
4. If tool calls present: execute handlers → add results → loop
5. If text/submit response present: parse and return

#### 2. Tool Calling (`agents/toolcall/claudetool/`)

Tools are defined as `ToolMetadata[Response]`:

```go
type ToolMetadata[Response any] struct {
    Definition anthropic.ToolParam
    Handler    func(ctx context.Context,
                   toolUse anthropic.ToolUseBlock,
                   trace *evals.Trace[Response],
                   result *Response) map[string]any
}
```

The handler function:
- Receives the tool use block with parameters
- Returns a `map[string]any` response (or error via `claudetool.Error()`)
- Can optionally set `result` to short-circuit and return immediately

Parameter extraction uses generic helpers:
```go
params, errResp := claudetool.NewParams(toolUse)
if errResp != nil {
    return errResp
}
filename, errResp := claudetool.Param[string](params, "filename")
```

#### 3. Prompt Builder (`agents/promptbuilder/`)

Prompts are templates with bindable placeholders:

```go
var systemInstructions = promptbuilder.MustNewPrompt(`
You are a CI fixing agent. Your job is to analyze CI failures and fix them.
`)

var userPrompt = promptbuilder.MustNewPrompt(`
Please fix the CI failures for this PR:
{{pr_context}}
`)
```

Request types implement `Bindable` to inject data:
```go
type PRContext struct {
    Owner    string `xml:"owner"`
    Repo     string `xml:"repo"`
    PRNumber int    `xml:"pr_number"`
    // ...
}

func (c *PRContext) Bind(prompt *promptbuilder.Prompt) (*promptbuilder.Prompt, error) {
    return prompt.BindXML("pr_context", c)
}
```

#### 4. Result Submission (`agents/submitresult/`)

The `submit_result` tool allows Claude to return structured data:

```go
type PRFixResult struct {
    Success      bool     `json:"success"`
    FixesApplied []string `json:"fixes_applied"`
    Reasoning    string   `json:"reasoning"`
}
```

The tool is configured via:
```go
claudeexecutor.WithSubmitResultProvider[Request, Response](
    submitresult.ClaudeToolForResponse[*PRFixResult],
)
```

#### 5. Schema Generation (`agents/schema/`)

JSON schemas are auto-generated from Go types using reflection:
```go
schema.ReflectType[MyToolInput]()
```

#### 6. Metrics (`agents/metrics/`)

OpenTelemetry integration tracks:
- Prompt tokens used
- Completion tokens used
- Tool calls made

#### 7. Tracing/Evals (`agents/evals/`)

Traces capture complete agent interactions:
- Input prompt and context
- Tool calls with parameters and results
- Reasoning content
- Final result or error
- Timing information

### GitHub Reconciler Infrastructure

The `reconcilers/githubreconciler/` package provides GitHub-specific infrastructure:

#### Clone Manager
Manages a pool of repository clones:
- Shallow clones of specific branches
- Fetch updates before each use
- Create work branches at specific commits
- Force push changes back to origin

#### Change Manager
Orchestrates PR lifecycle:
- Creates sessions for specific resources
- Queries for existing PRs with matching branches
- Manages PR creation/updates

#### Status Manager
Manages GitHub check runs:
- Creates/updates check run status
- Tracks observed state (last processed commit)
- Links to Cloud Logging for details

---

## The Evaluation Framework

Before integrating with GitHub, we must validate our agent through comprehensive evaluation. The go-driftlessaf framework provides a robust eval system.

### Core Concepts

#### Tracer Interface

The tracer captures agent execution for later evaluation:

```go
type Tracer[T any] interface {
    NewTrace(ctx context.Context, prompt string) *Trace[T]
    RecordTrace(trace *Trace[T])
}
```

#### Trace Structure

A trace represents a complete agent interaction:

```go
type Trace[T] struct {
    Prompt      string
    Result      T
    Error       error
    ToolCalls   []*ToolCall[T]
    Reasoning   string
    ExecContext *ExecutionContext
    StartTime   time.Time
    EndTime     time.Time
}
```

#### Observer Interface

Observers evaluate traces and report results:

```go
type Observer interface {
    Fail(msg string)           // Mark evaluation as failed
    Log(msg string)            // Log a message
    Grade(score float64, reasoning string)  // Assign 0.0-1.0 score
    Increment()                // Count this observation
    Total() int64              // Get total observations
}
```

### The Judge Component

The judge uses an LLM to evaluate agent responses against criteria.

#### Judge Interface

```go
type Interface interface {
    Judge(ctx context.Context, request *Request) (*Judgement, error)
}

type Request struct {
    Mode            JudgmentMode  // golden, benchmark, or standalone
    ReferenceAnswer string        // Expected answer (for golden mode)
    ActualAnswer    string        // Agent's actual response
    Criterion       string        // What to evaluate against
}

type Judgement struct {
    Score       float64   // 0.0 to 1.0 (or -1.0 to 1.0 for benchmark)
    Reasoning   string    // Explanation of the score
    Suggestions []string  // Improvement recommendations
    Mode        JudgmentMode
}
```

#### Judgment Modes

1. **Golden Mode**: Compare actual response against a known-good reference answer
   - Score range: 0.0 to 1.0
   - Use when you have example "correct" outputs

2. **Standalone Mode**: Evaluate response quality against criteria alone
   - Score range: 0.0 to 1.0
   - Use when there's no single correct answer

3. **Benchmark Mode**: Compare two responses against each other
   - Score range: -1.0 to 1.0
   - Negative favors first response, positive favors second

#### Creating a Judge

```go
// Using Vertex AI (Claude via Google Cloud)
judge, err := judge.NewVertex(ctx, projectID, region, "claude-sonnet-4-20250514")

// Or direct Anthropic API
judge, err := judge.NewClaude(ctx, apiKey, "claude-sonnet-4-20250514")
```

### Building Evaluation Callbacks

#### Using Golden Evals

For comparing against known-good responses:

```go
goldenEval := judge.NewGoldenEval[*CIFixResult](
    judgeInstance,
    "The fix should correctly address the CI failure with minimal changes",
    `{"success": true, "files_changed": ["src/main.go"], "reasoning": "Fixed type error"}`,
)
```

#### Using Standalone Evals

For evaluating without reference answers:

```go
standaloneEval := judge.NewStandaloneEval[*CIFixResult](
    judgeInstance,
    "The fix should be minimal, correct, and well-reasoned",
)
```

### Built-in Validation Helpers

The evals package provides many validation callbacks:

```go
// Tool call validation
evals.NoToolCalls[T]()                    // Ensure no tools were called
evals.ExactToolCalls[T](n)                // Exactly n tool calls
evals.MinimumNToolCalls[T](n)             // At least n tool calls
evals.MaximumNToolCalls[T](n)             // At most n tool calls
evals.RangeToolCalls[T](min, max)         // Between min and max
evals.OnlyToolCalls[T]("read_file", "write_file")  // Only these tools
evals.RequiredToolCalls[T]("read_file")   // Must use these tools

// Error validation
evals.NoErrors[T]()                       // No errors in trace

// Custom validation
evals.ResultValidator[T](func(result T) error { ... })
evals.ToolCallValidator[T](func(tc *ToolCall[T]) error { ... })
```

### Score Range Validation

Check that judge scores fall within expected ranges:

```go
// Validate score is in valid range for the mode
judge.ValidScore(judge.GoldenMode)

// Check score falls within expected quality band
judge.ScoreRange(0.7, 0.9)

// Ensure reasoning is present
judge.HasReasoning()

// Verify correct mode
judge.CheckMode(judge.GoldenMode)
```

### Running Evals with Test Integration

The `testevals` package integrates with Go's testing framework:

```go
func TestCIFixerAgent(t *testing.T) {
    ctx := context.Background()
    
    // Create observer that reports to testing.T
    obs := testevals.New(t)
    // Or with a prefix for namespacing
    obs := testevals.NewPrefix(t, "ci-fixer")
    
    // Build tracer with evaluation callbacks
    tracer := evals.BuildTracer(obs, map[string]evals.ObservableTraceCallback[*CIFixResult]{
        "no-errors":       evals.NoErrors[*CIFixResult](),
        "uses-read-file":  evals.RequiredToolCalls[*CIFixResult]("read_file"),
        "max-tool-calls":  evals.MaximumNToolCalls[*CIFixResult](10),
        "quality":         standaloneEval,
    })
    
    // Inject tracer into context
    ctx = evals.WithTracer(ctx, tracer)
    
    // Run agent
    result, err := agent.Execute(ctx, testCase.Input, tools)
    
    // Check results
    if err != nil {
        t.Fatalf("agent failed: %v", err)
    }
}
```

### Generating Reports

The report package generates formatted evaluation reports:

```go
// Create namespaced observer for hierarchical reporting
root := evals.NewNamespacedObserver[*evals.ResultCollector]()
obs := root.Child("ci-fixer").Child("type-errors")

// Run evaluations...

// Generate report with 0.8 threshold
reportStr, hasFailures := report.Simple(root, 0.8)
fmt.Println(reportStr)

if hasFailures {
    t.Fatal("Some evaluations fell below threshold")
}
```

Example output:
```
ci-fixer
├── type-errors
│   ├── no-errors: 100.0% (5/5)
│   ├── quality: 0.85 avg (5 results)
│   └── uses-read-file: 100.0% (5/5)
├── lint-errors
│   ├── no-errors: 80.0% (4/5)
│   │   └── 1: FAIL - Agent returned error on complex lint case
│   └── quality: 0.72 avg (5 results)
│       └── 2: 0.65 - Fix was correct but reasoning was unclear
```

---

## Evaluation Strategy for CI-Fixing Agent

### Phase 1: Unit Testing Tools (No LLM)

Test each tool in isolation:

```go
func TestReadFileTool(t *testing.T) {
    // Create mock file system
    fs := newMockFS(map[string]string{
        "main.go": "package main\n...",
    })
    
    tool := createReadFileTool(fs)
    
    // Test happy path
    result := tool.Handler(ctx, mockToolUse("read_file", map[string]any{
        "path": "main.go",
    }), nil, nil)
    
    if result["error"] != nil {
        t.Errorf("unexpected error: %v", result["error"])
    }
    if result["content"] != "package main\n..." {
        t.Errorf("wrong content")
    }
    
    // Test error cases
    result = tool.Handler(ctx, mockToolUse("read_file", map[string]any{
        "path": "../../../etc/passwd",
    }), nil, nil)
    
    if result["error"] == nil {
        t.Error("expected path traversal to be blocked")
    }
}
```

### Phase 2: Eval Test Cases

Create a corpus of test cases representing real CI failures:

```go
var evalTestCases = []struct {
    Name           string
    Description    string
    Files          map[string]string  // Repository state
    CILogs         string             // Failure logs
    ExpectedFix    map[string]string  // Expected file changes (golden)
    Criterion      string             // Evaluation criterion
}{
    {
        Name:        "simple-type-error",
        Description: "Missing return type annotation",
        Files: map[string]string{
            "main.go": `package main

func add(a, b int) {
    return a + b
}`,
        },
        CILogs: `main.go:4:2: too many return values
        have (int)
        want ()`,
        ExpectedFix: map[string]string{
            "main.go": `package main

func add(a, b int) int {
    return a + b
}`,
        },
        Criterion: "Fix should add the missing return type without changing function logic",
    },
    {
        Name:        "missing-import",
        Description: "Using fmt without importing it",
        Files: map[string]string{
            "main.go": `package main

func main() {
    fmt.Println("hello")
}`,
        },
        CILogs: `main.go:4:2: undefined: fmt`,
        ExpectedFix: map[string]string{
            "main.go": `package main

import "fmt"

func main() {
    fmt.Println("hello")
}`,
        },
        Criterion: "Fix should add the missing import statement",
    },
    // More test cases...
}
```

### Phase 3: Offline Evaluation Loop

Run evaluations without touching GitHub:

```go
func TestCIFixerEvals(t *testing.T) {
    ctx := context.Background()
    
    // Create judge for quality evaluation
    judgeInstance, err := judge.NewVertex(ctx, projectID, region, model)
    if err != nil {
        t.Fatalf("creating judge: %v", err)
    }
    
    // Create agent
    agent, err := newCIFixerAgent(ctx, cfg)
    if err != nil {
        t.Fatalf("creating agent: %v", err)
    }
    
    // Create namespaced observer for reporting
    root := evals.NewNamespacedObserver[*evals.ResultCollector]()
    
    for _, tc := range evalTestCases {
        t.Run(tc.Name, func(t *testing.T) {
            // Create child observer for this test case
            obs := root.Child("ci-fixer").Child(tc.Name)
            
            // Build golden eval
            goldenAnswer, _ := json.Marshal(tc.ExpectedFix)
            goldenEval := judge.NewGoldenEval[*CIFixResult](
                judgeInstance,
                tc.Criterion,
                string(goldenAnswer),
            )
            
            // Build tracer with evals
            tracer := evals.BuildTracer(obs, map[string]evals.ObservableTraceCallback[*CIFixResult]{
                "no-errors":      evals.NoErrors[*CIFixResult](),
                "reads-files":    evals.RequiredToolCalls[*CIFixResult]("read_file"),
                "writes-fix":     evals.RequiredToolCalls[*CIFixResult]("write_file"),
                "max-calls":      evals.MaximumNToolCalls[*CIFixResult](15),
                "golden-quality": goldenEval,
            })
            
            ctx := evals.WithTracer(ctx, tracer)
            
            // Create mock tools with test case files
            mockFS := newMockFS(tc.Files)
            tools := createTools(ctx, mockFS)
            
            // Build context
            ciCtx := &CIContext{
                Owner:    "test",
                Repo:     "test-repo",
                PRNumber: 1,
                Turn:     1,
                MaxTurns: 3,
                Failures: []CheckFailure{{
                    Name: "build",
                    Logs: tc.CILogs,
                }},
            }
            
            // Execute agent
            result, err := agent.Execute(ctx, ciCtx, tools)
            if err != nil {
                t.Errorf("agent error: %v", err)
            }
            
            // Verify files were changed correctly
            for path, expectedContent := range tc.ExpectedFix {
                actual, ok := mockFS.files[path]
                if !ok {
                    t.Errorf("expected file %s to be written", path)
                    continue
                }
                if actual != expectedContent {
                    t.Errorf("file %s content mismatch:\ngot:\n%s\nwant:\n%s", 
                        path, actual, expectedContent)
                }
            }
        })
    }
    
    // Generate final report
    reportStr, hasFailures := report.Simple(root, 0.8)
    t.Log("\n" + reportStr)
    
    if hasFailures {
        t.Error("Some evaluations fell below 0.8 threshold")
    }
}
```

### Phase 4: Prompt Iteration

Based on eval results, iterate on prompts:

1. **Analyze failures**: Look at low-scoring cases
2. **Identify patterns**: Common failure modes
3. **Update prompts**: Add guidance for failure modes
4. **Re-run evals**: Verify improvements
5. **Repeat**: Until quality threshold is met

Example prompt improvements based on eval feedback:

```go
// Before: Agent was making unnecessary changes
var systemInstructions = promptbuilder.MustNewPrompt(`
ROLE: You are a CI-fixing agent...
`)

// After: Added explicit guidance
var systemInstructions = promptbuilder.MustNewPrompt(`
ROLE: You are a CI-fixing agent...

CRITICAL RULES:
- ONLY modify lines directly related to the error
- DO NOT reformat code that isn't broken
- DO NOT add comments explaining your fix
- DO NOT change variable names unless they cause the error
- If the fix requires changing more than 10 lines, set needs_human=true
`)
```

### Phase 5: Regression Testing

Once prompts are stable, create a regression test suite:

```go
func TestCIFixerRegression(t *testing.T) {
    // Load golden test cases
    cases := loadGoldenTestCases(t, "testdata/golden/*.json")
    
    for _, tc := range cases {
        t.Run(tc.Name, func(t *testing.T) {
            // Run agent and compare to golden
            result := runAgent(t, tc.Input)
            
            // Exact match for deterministic cases
            if tc.Deterministic {
                assertEqual(t, tc.ExpectedOutput, result)
            }
            
            // Judge-based comparison for non-deterministic
            judgment := runJudge(t, tc.Criterion, tc.GoldenAnswer, result)
            if judgment.Score < 0.9 {
                t.Errorf("Quality regression: %.2f (was 0.9+)\n%s", 
                    judgment.Score, judgment.Reasoning)
            }
        })
    }
}
```

---

## Architecture for a CI-Fixing Agent

### High-Level Flow

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  GitHub Webhook │────▶│   Reconciler     │────▶│   CI-Fix Agent  │
│  (PR event)     │     │   (workqueue)    │     │   (Claude)      │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                                │                        │
                                │                        ▼
                                │                ┌───────────────┐
                                │                │  Tool Calls:  │
                                │                │  - read_file  │
                                │                │  - write_file │
                                │                │  - run_cmd    │
                                └───────────────▶│  - gh_api     │
                                                 └───────────────┘
                                                        │
                                                        ▼
                                                 ┌───────────────┐
                                                 │  git commit   │
                                                 │  git push     │
                                                 └───────────────┘
                                                        │
                                                        ▼
                                                 ┌───────────────┐
                                                 │  CI re-runs   │
                                                 │  (loop back)  │
                                                 └───────────────┘
```

### Event-Driven Turn Loop (Async Architecture)

Rather than waiting synchronously for CI to complete, the CI fixer uses an **event-driven architecture** that leverages the reconciler's natural event loop:

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  PR Created  │────▶│  Reconciler  │────▶│  CI Fixer    │
│  or Updated  │     │  (Turn 1)    │     │  pushes fix  │
└──────────────┘     └──────────────┘     └──────┬───────┘
                                                  │
                                                  ▼
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Workflow    │◀────│   CI runs    │◀────│  New commit  │
│  completes   │     │   on fix     │     │  on branch   │
└──────┬───────┘     └──────────────┘     └──────────────┘
       │
       ▼
┌──────────────┐     ┌──────────────┐
│  Reconciler  │────▶│  CI Fixer    │
│  (Turn 2)    │     │  reassesses  │
└──────────────┘     └──────────────┘
```

**Key principles:**

1. **No synchronous waiting**: The reconciler pushes a fix and returns immediately
2. **State persistence**: Turn count is tracked in `ReconcilerDetails` and persisted in the GitHub Check Run
3. **Event-driven triggers**: GitHub workflow completion events trigger a new reconciliation
4. **Idempotent processing**: Each reconciliation checks if work is needed before acting

**How turn tracking works:**

```go
// On each reconciliation:
// 1. Get existing check run to retrieve previous turn count
existingStatus, _ := session.ObservedState(ctx)
previousTurn := 0
if existingStatus != nil {
    previousTurn = existingStatus.Details.CIFixTurns
}

// 2. Check if CI is now passing
if allChecksPassing(checks) {
    // Success! Update status and return
    return nil
}

// 3. Check if we've exhausted turns
currentTurn := previousTurn + 1
if currentTurn > maxTurns {
    // Max turns reached, give up
    return nil
}

// 4. Run the agent for this turn
result, err := agent.Execute(ctx, &CIContext{
    Turn:     currentTurn,
    MaxTurns: maxTurns,
    // ...
}, tools)

// 5. If agent made changes, commit, push, and return
if len(result.FilesChanged) > 0 {
    commitAndPush(ctx, clone, result.CommitMessage)
    // Store current turn in status for next reconciliation
    details.CIFixTurns = currentTurn
}
// Return immediately - don't wait for CI
```

**Advantages of this approach:**

- **Resource efficient**: No long-running processes waiting for CI
- **Scalable**: Reconciler can handle many PRs concurrently  
- **Resilient**: If reconciler crashes, state is preserved in GitHub Check Run
- **Natural backpressure**: Workqueue handles rate limiting

**Turn state transitions:**

| Previous State | CI Status | Action |
|---------------|-----------|--------|
| Turn 0 (new) | Failing | Run Turn 1, push fix |
| Turn N | Passing | Mark success, done |
| Turn N | Failing, N < max | Run Turn N+1, push fix |
| Turn N | Failing, N >= max | Mark failure, give up |
| Turn N | Pending | Skip (wait for completion) |

### Request/Response Types

```go
type CIContext struct {
    Owner      string              `xml:"owner"`
    Repo       string              `xml:"repo"`
    PRNumber   int                 `xml:"pr_number"`
    Branch     string              `xml:"branch"`
    Turn       int                 `xml:"turn"`
    MaxTurns   int                 `xml:"max_turns"`
    Failures   []CheckFailure      `xml:"failures>failure"`
    ChangedFiles []string          `xml:"changed_files>file"`
}

func (c *CIContext) Bind(prompt *promptbuilder.Prompt) (*promptbuilder.Prompt, error) {
    return prompt.BindXML("ci_context", c)
}

type CIFixResult struct {
    Success       bool     `json:"success"`
    FilesChanged  []string `json:"files_changed"`
    CommitMessage string   `json:"commit_message"`
    Reasoning     string   `json:"reasoning"`
    NeedsHuman    bool     `json:"needs_human"`
}
```

### Tools for the CI-Fixing Agent

#### 1. `read_file` - Read file contents
```go
type ReadFileInput struct {
    Path string `json:"path" jsonschema:"description=Path to the file relative to repo root"`
}
```

#### 2. `write_file` - Write/modify file contents
```go
type WriteFileInput struct {
    Path    string `json:"path" jsonschema:"description=Path to the file"`
    Content string `json:"content" jsonschema:"description=New file content"`
}
```

#### 3. `run_command` - Execute shell commands (sandboxed)
```go
type RunCommandInput struct {
    Command string   `json:"command" jsonschema:"description=Command to run"`
    Args    []string `json:"args" jsonschema:"description=Command arguments"`
}
```

#### 4. `list_files` - List directory contents
```go
type ListFilesInput struct {
    Path    string `json:"path" jsonschema:"description=Directory path"`
    Pattern string `json:"pattern" jsonschema:"description=Glob pattern"`
}
```

#### 5. `search_files` - Search file contents (grep)
```go
type SearchFilesInput struct {
    Pattern string `json:"pattern" jsonschema:"description=Search pattern (regex)"`
    Path    string `json:"path" jsonschema:"description=Directory to search"`
}
```

#### 6. `get_check_logs` - Fetch CI check logs
```go
type GetCheckLogsInput struct {
    CheckRunID int64 `json:"check_run_id" jsonschema:"description=GitHub check run ID"`
}
```

### System Prompt

```go
var systemInstructions = promptbuilder.MustNewPrompt(`
ROLE: You are a CI-fixing agent. Your job is to analyze CI failures and fix them by modifying code.

CONTEXT: You are operating on turn {{turn}} of {{max_turns}} maximum turns. If you cannot fix the issue within the remaining turns, set needs_human=true and explain why.

WORKFLOW:
1. Analyze the CI failure logs to understand what's failing
2. Use read_file to examine relevant source files
3. Use search_files to find related code if needed
4. Determine the root cause of the failure
5. Use write_file to make fixes
6. Submit your result with a clear commit message

GUIDELINES:
- Make minimal, targeted changes
- Do not refactor unrelated code
- If the fix requires architectural changes, set needs_human=true
- Always explain your reasoning
- Test your understanding by reading the code before making changes

CRITICAL RULES:
- ONLY modify lines directly related to the error
- DO NOT reformat code that isn't broken
- DO NOT add comments explaining your fix
- DO NOT change variable names unless they cause the error
- If the fix requires changing more than 10 lines, set needs_human=true

COMMON FAILURE TYPES:
- Type errors: Check type annotations and imports
- Test failures: Read the test and the code it tests
- Lint errors: Check the lint rule and fix accordingly
- Build errors: Check dependencies and imports
- Formatting: Run the appropriate formatter
`)
```

### Agent Creation

```go
func newCIFixerAgent(ctx context.Context, cfg *config) (claudeexecutor.Interface[*CIContext, *CIFixResult], error) {
    client := anthropic.NewClient(
        vertex.WithGoogleAuth(ctx, cfg.GCPRegion, cfg.GCPProjectID),
    )

    return claudeexecutor.New[*CIContext, *CIFixResult](
        client,
        userPrompt,
        claudeexecutor.WithModel[*CIContext, *CIFixResult](cfg.ClaudeModel),
        claudeexecutor.WithMaxTokens[*CIContext, *CIFixResult](16384),
        claudeexecutor.WithTemperature[*CIContext, *CIFixResult](0.1),
        claudeexecutor.WithSystemInstructions[*CIContext, *CIFixResult](systemInstructions),
        claudeexecutor.WithSubmitResultProvider[*CIContext, *CIFixResult](
            submitresult.ClaudeToolForResponse[*CIFixResult],
        ),
    )
}
```

### Tool Implementation Example

```go
func createTools(ctx context.Context, clone *clonemanager.Clone) map[string]claudeexecutor.ToolMetadata[*CIFixResult] {
    return map[string]claudeexecutor.ToolMetadata[*CIFixResult]{
        "read_file": {
            Definition: anthropic.ToolParam{
                Name:        "read_file",
                Description: "Read the contents of a file in the repository",
                InputSchema: schema.ReflectType[ReadFileInput](),
            },
            Handler: func(ctx context.Context, toolUse anthropic.ToolUseBlock,
                          trace *evals.Trace[*CIFixResult], result **CIFixResult) map[string]any {
                params, errResp := claudetool.NewParams(toolUse)
                if errResp != nil {
                    return errResp
                }
                path, errResp := claudetool.Param[string](params, "path")
                if errResp != nil {
                    return errResp
                }

                content, err := clone.ReadFile(path)
                if err != nil {
                    return claudetool.Error("failed to read file: %v", err)
                }

                return map[string]any{
                    "content": content,
                }
            },
        },
        "write_file": {
            Definition: anthropic.ToolParam{
                Name:        "write_file",
                Description: "Write content to a file in the repository",
                InputSchema: schema.ReflectType[WriteFileInput](),
            },
            Handler: func(ctx context.Context, toolUse anthropic.ToolUseBlock,
                          trace *evals.Trace[*CIFixResult], result **CIFixResult) map[string]any {
                params, errResp := claudetool.NewParams(toolUse)
                if errResp != nil {
                    return errResp
                }
                path, errResp := claudetool.Param[string](params, "path")
                if errResp != nil {
                    return errResp
                }
                content, errResp := claudetool.Param[string](params, "content")
                if errResp != nil {
                    return errResp
                }

                if err := clone.WriteFile(path, content); err != nil {
                    return claudetool.Error("failed to write file: %v", err)
                }

                return map[string]any{
                    "success": true,
                    "path":    path,
                }
            },
        },
        // ... more tools
    }
}
```

### Main Reconciler Loop (Async Pattern)

The reconciler processes a single turn per invocation and returns immediately after pushing a fix:

```go
func runCIFixer(ctx context.Context, gh *github.Client, agent claudeexecutor.Interface[*CIContext, *CIFixResult],
                owner, repo string, number int, pr *github.PullRequest, 
                existingDetails *ReconcilerDetails, cfg *config) (*ReconcilerDetails, error) {

    details := &ReconcilerDetails{CIFixAttempted: true}
    
    // Get previous turn count from existing state (persisted in check run)
    previousTurn := 0
    if existingDetails != nil {
        previousTurn = existingDetails.CIFixTurns
    }

    // Get check runs for the PR head
    sha := pr.GetHead().GetSHA()
    checks, _, err := gh.Checks.ListCheckRunsForRef(ctx, owner, repo, sha, nil)
    if err != nil {
        return nil, fmt.Errorf("listing checks: %w", err)
    }

    // Count check states to determine if we should act
    pending, failed, passed := countCheckStates(checks.CheckRuns)
    
    // If checks are still pending, skip this reconciliation
    // (we'll be triggered again when they complete)
    if pending > 0 {
        clog.InfoContextf(ctx, "CI still pending (%d checks), skipping", pending)
        details.CIFixReasoning = "Waiting for CI to complete"
        return details, nil
    }

    // If all checks passing, we're done!
    if failed == 0 {
        clog.InfoContextf(ctx, "All %d checks passing", passed)
        details.CIFixSuccess = true
        details.CIFixTurns = previousTurn
        details.CIFixReasoning = "CI is now passing"
        return details, nil
    }

    // Calculate current turn
    currentTurn := previousTurn + 1
    if currentTurn > cfg.MaxTurns {
        clog.WarnContextf(ctx, "Max turns (%d) reached", cfg.MaxTurns)
        details.CIFixTurns = previousTurn
        details.CIFixReasoning = fmt.Sprintf("Max turns (%d) reached without fixing CI", cfg.MaxTurns)
        return details, nil
    }

    clog.InfoContextf(ctx, "Running CI fixer turn %d/%d", currentTurn, cfg.MaxTurns)

    // Clone repo, run agent, push fix...
    // (implementation details omitted for brevity)

    result, err := agent.Execute(ctx, ciCtx, tools)
    if err != nil {
        return nil, fmt.Errorf("agent execution: %w", err)
    }

    details.CIFixTurns = currentTurn

    if result.NeedsHuman {
        details.CIFixNeedsHuman = true
        details.CIFixReasoning = result.Reasoning
        return details, nil
    }

    if len(result.FilesChanged) > 0 {
        // Commit and push
        if err := clone.CommitAndPush(ctx, result.CommitMessage); err != nil {
            return nil, fmt.Errorf("commit and push: %w", err)
        }
        details.CIFixFiles = result.FilesChanged
        details.CIFixCommit = clone.SHA()
        details.CIFixReasoning = result.Reasoning
        
        // Return immediately - don't wait for CI!
        // The workqueue will trigger a new reconciliation when
        // GitHub workflow events fire on the new commit.
    }

    return details, nil
}
```

---

## Implementation Considerations

### Security

1. **Sandboxed command execution**: Only allow specific commands (go build, go test, npm test, etc.)
2. **Path validation**: Ensure file operations stay within the repository
3. **Token scoping**: Use minimal GitHub token permissions
4. **Rate limiting**: Respect GitHub API limits and add jitter to retries

### Observability

1. **OpenTelemetry metrics**: Track tokens used, tool calls, turn counts
2. **Structured logging**: Use clog for context-aware logging
3. **Traces**: Record full agent interactions for debugging and evals
4. **Check run details URL**: Link to Cloud Logging for each reconciliation

### Reliability

1. **Turn limits**: Prevent infinite loops with configurable max turns
2. **CI timeout**: Don't wait forever for CI to complete
3. **Graceful degradation**: If agent fails, don't break the PR
4. **Idempotency**: Handle re-reconciliation of the same PR state

### Cost Management

1. **Low temperature**: Use 0.1 to reduce variance
2. **Token limits**: Set reasonable max_tokens
3. **Caching**: Cache file reads within a turn
4. **Early exit**: Submit result as soon as fix is determined

---

## Development Phases

### Phase 1: Foundation (No LLM calls)

1. **Set up the project structure**
   - Create Go module with proper dependencies
   - Set up Terraform for Cloud Run deployment

2. **Implement core components**
   - Mock file system for testing
   - Tool implementations (read_file, write_file, run_command, etc.)
   - CI status checking and log fetching (mocked)

3. **Unit tests for all tools**
   - Happy path tests
   - Error handling tests
   - Security tests (path traversal, command injection)

### Phase 2: Offline Evaluation (LLM calls, no GitHub)

1. **Create eval test corpus**
   - Collect real CI failure examples
   - Create golden answers for each
   - Define evaluation criteria

2. **Implement evaluation harness**
   - Set up judge integration
   - Build tracer and observer infrastructure
   - Create report generation

3. **Run initial evaluations**
   - Baseline prompt performance
   - Identify failure patterns
   - Document quality metrics

4. **Iterate on prompts**
   - Address common failures
   - Add specific guidance
   - Re-run evals after each change
   - Target: 0.8+ average score

5. **Regression test suite**
   - Lock in high-quality test cases
   - Automated CI for prompt changes

### Phase 3: Integration Testing (GitHub, staging repo)

1. **Create test repository**
   - Intentionally broken code
   - Various CI failure types
   - Controlled environment

2. **Implement GitHub integration**
   - Clone manager
   - Commit and push
   - Check run monitoring

3. **End-to-end testing**
   - Real CI failures → real fixes
   - Monitor turn counts
   - Verify commit quality

### Phase 4: Production Deployment

1. **Deploy to Cloud Run**
   - Terraform configuration
   - GitHub App setup
   - Workqueue integration

2. **Gradual rollout**
   - Start with single repo
   - Monitor metrics closely
   - Expand based on success

3. **Continuous improvement**
   - Collect production traces
   - Run evals on real cases
   - Iterate on prompts

---

## Event Handling for CI Completion

### The check_run Event Problem

The CI fixer needs to be notified when CI checks complete so it can re-evaluate failing PRs. However, there's a complication with how GitHub events are processed:

**The issue:** The `github-events` Terraform module from `terraform-infra-common` does NOT set the `pullrequesturl` CloudEvents extension for `check_run` events. The relevant code in the trampoline is commented out:

```go
// case "check_run":
// 	if len(info.CheckRun.PullRequests) > 0 {
// 		prNumber = info.CheckRun.PullRequests[0].Number
// 	}
```

This means `check_run` events don't match the primary workqueue filter (which uses `extension_key = "pullrequesturl"`), so they never trigger reconciliation.

### Solution: Separate check_run Workqueue

We deploy a second workqueue specifically for `check_run` events:

```hcl
# In trigger.tf
module "check-run-workqueue" {
  source = "chainguard-dev/common/infra//modules/cloudevents-workqueue"

  # Filter for completed check_run events
  filters = [{
    attributes = {
      type   = "dev.chainguard.github.check_run"
      action = "completed"
    }
  }]

  # Use repo URL as key (check_run doesn't have pullrequesturl)
  extension_key = "repo"

  workqueue = {
    name = module.pr-reconciler.receiver.name
  }
}
```

### How the Reconciler Handles check_run Events

When the reconciler receives a repo-keyed event (not a PR URL), it:

1. **Detects the key format**: Distinguishes `https://github.com/owner/repo` from `https://github.com/owner/repo/pull/123`

2. **Lists relevant PRs**: Queries for open PRs in that repo with the `ci-autofix` label

3. **Re-enqueues them**: Returns the PR URLs in `ProcessResponse.Requeue` so they get reconciled

```go
func (r *PRReconciler) processCheckRunEvent(ctx context.Context, owner, repo string) (*workqueue.ProcessResponse, error) {
    // List open PRs with ci-autofix label
    prs, _, err := gh.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
        State: "open",
    })

    // Filter to PRs with the label and re-queue them
    var requeue []string
    for _, pr := range prs {
        if hasLabel(pr.Labels, r.cfg.CIFixerLabel) {
            requeue = append(requeue, pr.GetHTMLURL())
        }
    }

    return &workqueue.ProcessResponse{Requeue: requeue}, nil
}
```

### Event Flow Diagram

```
┌───────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  CI Workflow  │────▶│  check_run event │────▶│ check-run-queue │
│  completes    │     │  (repo-keyed)    │     │                 │
└───────────────┘     └──────────────────┘     └────────┬────────┘
                                                        │
                                                        ▼
                                               ┌─────────────────┐
                                               │  Reconciler     │
                                               │  (processCheck  │
                                               │   RunEvent)     │
                                               └────────┬────────┘
                                                        │
                                            Lists PRs with ci-autofix label
                                                        │
                                                        ▼
┌───────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Reconciler   │◀────│  PR URLs         │◀────│  Requeue PRs    │
│  (PR process) │     │  re-enqueued     │     │                 │
└───────────────┘     └──────────────────┘     └─────────────────┘
```

### Why Not Patch terraform-infra-common?

While uncommenting the `check_run` case in `terraform-infra-common` would be cleaner, this approach:

1. **Doesn't require upstream changes** - Works with the module as-is
2. **Is explicit** - The separate workqueue makes the event handling obvious
3. **Is flexible** - We can add custom filtering (e.g., only certain check names)

If the upstream module is eventually updated to set `pullrequesturl` for `check_run` events, this separate workqueue can be removed.

---

## Quality Gates

Before each phase transition, ensure:

| Phase | Gate Criteria |
|-------|---------------|
| 1 → 2 | 100% unit test coverage for tools, all security tests passing |
| 2 → 3 | 0.8+ average eval score, <5% failure rate, regression suite green |
| 3 → 4 | 10+ successful end-to-end fixes in staging, no security issues |
| 4 → production | Human review of first 20 production fixes, metrics dashboard ready |
