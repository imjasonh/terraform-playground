package main

import (
	"strings"
	"testing"
)

func TestEvaluateCIStatus(t *testing.T) {
	for _, tt := range []struct {
		desc         string
		pending      int
		passed       int
		failed       int
		previousTurn int
		wantState    ciState
		wantContains string // substring that should be in reasoning
	}{{
		desc:         "no checks yet - CI not started",
		pending:      0,
		passed:       0,
		failed:       0,
		previousTurn: 0,
		wantState:    ciStateNoChecks,
		wantContains: "Waiting for CI checks to start",
	}, {
		desc:         "no checks yet - after previous turn",
		pending:      0,
		passed:       0,
		failed:       0,
		previousTurn: 1,
		wantState:    ciStateNoChecks,
		wantContains: "Waiting for CI checks to start",
	}, {
		desc:         "checks pending - one running",
		pending:      1,
		passed:       0,
		failed:       0,
		previousTurn: 0,
		wantState:    ciStatePending,
		wantContains: "1 CI checks to complete",
	}, {
		desc:         "checks pending - multiple running",
		pending:      3,
		passed:       2,
		failed:       0,
		previousTurn: 0,
		wantState:    ciStatePending,
		wantContains: "3 CI checks to complete",
	}, {
		desc:         "checks pending with some failed - still wait",
		pending:      1,
		passed:       1,
		failed:       1,
		previousTurn: 0,
		wantState:    ciStatePending,
		wantContains: "1 CI checks to complete",
	}, {
		desc:         "all passing - no previous turns",
		pending:      0,
		passed:       3,
		failed:       0,
		previousTurn: 0,
		wantState:    ciStatePassing,
		wantContains: "CI is passing",
	}, {
		desc:         "all passing - after 1 fix",
		pending:      0,
		passed:       3,
		failed:       0,
		previousTurn: 1,
		wantState:    ciStatePassing,
		wantContains: "after 1 fix attempt",
	}, {
		desc:         "all passing - after multiple fixes",
		pending:      0,
		passed:       3,
		failed:       0,
		previousTurn: 3,
		wantState:    ciStatePassing,
		wantContains: "after 3 fix attempt",
	}, {
		desc:         "one check failed",
		pending:      0,
		passed:       2,
		failed:       1,
		previousTurn: 0,
		wantState:    ciStateFailing,
		wantContains: "1 CI checks failed",
	}, {
		desc:         "multiple checks failed",
		pending:      0,
		passed:       1,
		failed:       2,
		previousTurn: 0,
		wantState:    ciStateFailing,
		wantContains: "2 CI checks failed",
	}, {
		desc:         "all checks failed",
		pending:      0,
		passed:       0,
		failed:       3,
		previousTurn: 0,
		wantState:    ciStateFailing,
		wantContains: "3 CI checks failed",
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			result := evaluateCIStatus(tt.pending, tt.passed, tt.failed, tt.previousTurn)

			if result.State != tt.wantState {
				t.Errorf("state: got %v, want %v", result.State, tt.wantState)
			}

			if !strings.Contains(result.Reasoning, tt.wantContains) {
				t.Errorf("reasoning: got %q, want it to contain %q", result.Reasoning, tt.wantContains)
			}
		})
	}
}

func TestReconcilerDetailsMarkdown(t *testing.T) {
	for _, tt := range []struct {
		desc     string
		details  ReconcilerDetails
		contains []string
		excludes []string
	}{{
		desc:     "empty details shows nothing",
		details:  ReconcilerDetails{},
		excludes: []string{"Auto-Labeler", "CI Fixer"},
	}, {
		desc: "auto-labeler only",
		details: ReconcilerDetails{
			FilesAnalyzed: 5,
			TotalChanges:  100,
			LabelsApplied: []string{"lang/go", "size/S"},
		},
		contains: []string{
			"Auto-Labeler",
			"5 files",
			"100 lines",
			"`lang/go`",
			"`size/S`",
		},
		excludes: []string{"CI Fixer"},
	}, {
		desc: "CI pending - waiting for checks to start",
		details: ReconcilerDetails{
			CIFixPending:   true,
			CIFixReasoning: "Waiting for CI checks to start",
		},
		contains: []string{
			"CI Fixer",
			"⏳",
			"Waiting for CI",
		},
		excludes: []string{"✅", "❌", "⚠️"},
	}, {
		desc: "CI pending - checks running",
		details: ReconcilerDetails{
			CIFixPending:   true,
			CIFixReasoning: "Waiting for 2 CI checks to complete",
		},
		contains: []string{
			"CI Fixer",
			"⏳",
			"Waiting for CI to complete",
			"2 CI checks",
		},
		excludes: []string{"✅", "❌", "⚠️"},
	}, {
		desc: "CI passing - no fix needed",
		details: ReconcilerDetails{
			CIFixSuccess:   true,
			CIFixTurns:     0,
			CIFixReasoning: "CI is passing",
		},
		contains: []string{
			"CI Fixer",
			"✅",
			"CI is passing",
			"No fixes were needed",
		},
		excludes: []string{"❌", "⚠️", "⏳", "after fix"},
	}, {
		desc: "CI passing after fix",
		details: ReconcilerDetails{
			CIFixAttempted: true,
			CIFixSuccess:   true,
			CIFixTurns:     2,
			CIFixFiles:     []string{"main.go", "utils.go"},
			CIFixCommit:    "abc123def",
			CIFixReasoning: "Fixed type error",
		},
		contains: []string{
			"CI Fixer",
			"✅",
			"CI passing after fix",
			"Turns used: 2",
			"`main.go`",
			"`utils.go`",
			"abc123def",
		},
		excludes: []string{"❌", "⚠️", "⏳", "No fixes were needed"},
	}, {
		desc: "CI fix attempted but failed",
		details: ReconcilerDetails{
			CIFixAttempted: true,
			CIFixSuccess:   false,
			CIFixTurns:     3,
			CIFixReasoning: "Could not determine fix for lint error",
		},
		contains: []string{
			"CI Fixer",
			"❌",
			"Could not fix CI failure",
			"Could not determine fix",
			"Turns attempted: 3",
		},
		excludes: []string{"✅", "⚠️", "⏳"},
	}, {
		desc: "human intervention needed",
		details: ReconcilerDetails{
			CIFixAttempted:  true,
			CIFixNeedsHuman: true,
			CIFixTurns:      1,
			CIFixReasoning:  "Fix requires architectural changes",
		},
		contains: []string{
			"CI Fixer",
			"⚠️",
			"Human intervention requested",
			"architectural changes",
			"Turns attempted: 1",
		},
		excludes: []string{"✅", "❌", "⏳"},
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			md := tt.details.Markdown()

			for _, want := range tt.contains {
				if !strings.Contains(md, want) {
					t.Errorf("expected markdown to contain %q, got:\n%s", want, md)
				}
			}

			for _, exclude := range tt.excludes {
				if strings.Contains(md, exclude) {
					t.Errorf("expected markdown to NOT contain %q, got:\n%s", exclude, md)
				}
			}
		})
	}
}

func TestParsePRURL(t *testing.T) {
	for _, tt := range []struct {
		desc      string
		url       string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantErr   bool
	}{{
		desc:      "valid PR URL",
		url:       "https://github.com/owner/repo/pull/123",
		wantOwner: "owner",
		wantRepo:  "repo",
		wantNum:   123,
	}, {
		desc:      "valid PR URL with dashes",
		url:       "https://github.com/my-org/my-repo/pull/456",
		wantOwner: "my-org",
		wantRepo:  "my-repo",
		wantNum:   456,
	}, {
		desc:    "repo URL is not a PR URL",
		url:     "https://github.com/owner/repo",
		wantErr: true,
	}, {
		desc:    "issue URL is not a PR URL",
		url:     "https://github.com/owner/repo/issues/123",
		wantErr: true,
	}, {
		desc:    "empty string",
		url:     "",
		wantErr: true,
	}, {
		desc:    "random string",
		url:     "not a url",
		wantErr: true,
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			owner, repo, num, err := parsePRURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got owner=%q repo=%q num=%d", owner, repo, num)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if owner != tt.wantOwner {
				t.Errorf("owner: got %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo: got %q, want %q", repo, tt.wantRepo)
			}
			if num != tt.wantNum {
				t.Errorf("number: got %d, want %d", num, tt.wantNum)
			}
		})
	}
}
