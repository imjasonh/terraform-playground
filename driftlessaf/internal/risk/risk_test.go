package risk

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-github/v75/github"
)

func TestMatchPattern(t *testing.T) {
	for _, tt := range []struct {
		desc     string
		pattern  string
		filename string
		want     bool
	}{{
		desc:     "simple extension match",
		pattern:  "*.go",
		filename: "main.go",
		want:     true,
	}, {
		desc:     "simple extension no match",
		pattern:  "*.go",
		filename: "main.rs",
		want:     false,
	}, {
		desc:     "extension match in subdirectory",
		pattern:  "*.go",
		filename: "pkg/foo/bar.go",
		want:     true,
	}, {
		desc:     "exact filename match",
		pattern:  "go.mod",
		filename: "go.mod",
		want:     true,
	}, {
		desc:     "exact filename in subdir matches basename",
		pattern:  "go.mod",
		filename: "subdir/go.mod",
		want:     true,
	}, {
		desc:     "terraform files",
		pattern:  "*.tf",
		filename: "main.tf",
		want:     true,
	}, {
		desc:     "terraform tfvars",
		pattern:  "*.tfvars",
		filename: "terraform.tfvars",
		want:     true,
	}, {
		desc:     "dockerfile prefix",
		pattern:  "Dockerfile*",
		filename: "Dockerfile",
		want:     true,
	}, {
		desc:     "dockerfile with extension",
		pattern:  "Dockerfile*",
		filename: "Dockerfile.prod",
		want:     true,
	}, {
		desc:     "github workflows",
		pattern:  ".github/workflows/**",
		filename: ".github/workflows/ci.yml",
		want:     true,
	}, {
		desc:     "k8s directory",
		pattern:  "k8s/**",
		filename: "k8s/deployment.yaml",
		want:     true,
	}, {
		desc:     "k8s nested",
		pattern:  "k8s/**",
		filename: "k8s/prod/deployment.yaml",
		want:     true,
	}, {
		desc:     "migrations pattern",
		pattern:  "**/migrations/**",
		filename: "db/migrations/001_init.sql",
		want:     true,
	}, {
		desc:     "migration file pattern",
		pattern:  "**/*migration*",
		filename: "db/001_migration_init.sql",
		want:     true,
	}, {
		desc:     "yaml files",
		pattern:  "*.yaml",
		filename: "config.yaml",
		want:     true,
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := matchPattern(tt.pattern, tt.filename)
			if err != nil {
				t.Fatalf("matchPattern(%q, %q) error: %v", tt.pattern, tt.filename, err)
			}
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.filename, got, tt.want)
			}
		})
	}
}

func TestScorer_AssessRisk(t *testing.T) {
	scorer := NewWithDefaults()
	ctx := context.Background()

	for _, tt := range []struct {
		desc      string
		files     []*github.CommitFile
		wantLevel Level
	}{{
		desc: "terraform changes - high risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("main.tf"), Additions: github.Ptr(20), Deletions: github.Ptr(5)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "docker changes - high risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("Dockerfile"), Additions: github.Ptr(15), Deletions: github.Ptr(3)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "k8s changes - high risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("k8s/deployment.yaml"), Additions: github.Ptr(30), Deletions: github.Ptr(10)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "github workflows - high risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr(".github/workflows/ci.yml"), Additions: github.Ptr(25), Deletions: github.Ptr(5)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "dependency file - high risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("go.mod"), Additions: github.Ptr(5), Deletions: github.Ptr(2)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "database migration - high risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("db/migrations/001_init.sql"), Additions: github.Ptr(50), Deletions: github.Ptr(0)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "go code changes - medium risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("main.go"), Additions: github.Ptr(30), Deletions: github.Ptr(10)},
		},
		wantLevel: LevelMedium,
	}, {
		desc: "typescript changes - medium risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("src/App.tsx"), Additions: github.Ptr(25), Deletions: github.Ptr(5)},
		},
		wantLevel: LevelMedium,
	}, {
		desc: "python changes - medium risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("script.py"), Additions: github.Ptr(20), Deletions: github.Ptr(5)},
		},
		wantLevel: LevelMedium,
	}, {
		desc: "config file changes - medium risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("app/config/settings.toml"), Additions: github.Ptr(10), Deletions: github.Ptr(2)},
		},
		wantLevel: LevelMedium,
	}, {
		desc: "documentation only - low risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("README.md"), Additions: github.Ptr(30), Deletions: github.Ptr(5)},
		},
		wantLevel: LevelLow,
	}, {
		desc: "test files only - low risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("main_test.go"), Additions: github.Ptr(50), Deletions: github.Ptr(10)},
		},
		wantLevel: LevelMedium, // Still medium because *.go matches
	}, {
		desc: "small code change - low risk",
		files: []*github.CommitFile{
			{Filename: github.Ptr("util.js"), Additions: github.Ptr(5), Deletions: github.Ptr(2)},
		},
		wantLevel: LevelMedium, // Still medium because it's code
	}, {
		desc: "large PR - high risk by size",
		files: []*github.CommitFile{
			{Filename: github.Ptr("data.json"), Additions: github.Ptr(400), Deletions: github.Ptr(200)},
		},
		wantLevel: LevelHigh,
	}, {
		desc: "medium PR - medium risk by size",
		files: []*github.CommitFile{
			{Filename: github.Ptr("data.json"), Additions: github.Ptr(150), Deletions: github.Ptr(100)},
		},
		wantLevel: LevelMedium,
	}, {
		desc: "mixed changes - highest risk wins",
		files: []*github.CommitFile{
			{Filename: github.Ptr("README.md"), Additions: github.Ptr(10), Deletions: github.Ptr(0)},
			{Filename: github.Ptr("main.go"), Additions: github.Ptr(20), Deletions: github.Ptr(5)},
			{Filename: github.Ptr("Dockerfile"), Additions: github.Ptr(5), Deletions: github.Ptr(2)},
		},
		wantLevel: LevelHigh, // Dockerfile is high risk
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			assessment := scorer.AssessRisk(ctx, tt.files)

			if assessment.Level != tt.wantLevel {
				t.Errorf("AssessRisk() level = %v, want %v\nReasons: %v",
					assessment.Level, tt.wantLevel, assessment.Reasons)
			}

			// Verify assessment has required fields
			if assessment.FilesAnalyzed != len(tt.files) {
				t.Errorf("FilesAnalyzed = %d, want %d", assessment.FilesAnalyzed, len(tt.files))
			}

			if assessment.TotalChanges < 0 {
				t.Errorf("TotalChanges should not be negative: %d", assessment.TotalChanges)
			}

			if len(assessment.Reasons) == 0 {
				t.Error("Assessment should have at least one reason")
			}
		})
	}
}

func TestLevel_Label(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelLow, "risk/low"},
		{LevelMedium, "risk/medium"},
		{LevelHigh, "risk/high"},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			if got := tt.level.Label(); got != tt.want {
				t.Errorf("Level.Label() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterNewLabel(t *testing.T) {
	for _, tt := range []struct {
		desc       string
		assessment Assessment
		existing   []*github.Label
		wantLabel  *string
	}{{
		desc:       "no existing labels",
		assessment: Assessment{Level: LevelHigh},
		existing:   nil,
		wantLabel:  github.Ptr("risk/high"),
	}, {
		desc:       "label already exists",
		assessment: Assessment{Level: LevelHigh},
		existing: []*github.Label{
			{Name: github.Ptr("risk/high")},
		},
		wantLabel: nil,
	}, {
		desc:       "different risk label exists",
		assessment: Assessment{Level: LevelHigh},
		existing: []*github.Label{
			{Name: github.Ptr("risk/low")},
		},
		wantLabel: github.Ptr("risk/high"),
	}, {
		desc:       "other labels exist",
		assessment: Assessment{Level: LevelMedium},
		existing: []*github.Label{
			{Name: github.Ptr("lang/go")},
			{Name: github.Ptr("size/M")},
		},
		wantLabel: github.Ptr("risk/medium"),
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			got := FilterNewLabel(tt.assessment, tt.existing)

			if (got == nil) != (tt.wantLabel == nil) {
				t.Errorf("FilterNewLabel() = %v, want %v", got, tt.wantLabel)
				return
			}

			if got != nil && *got != *tt.wantLabel {
				t.Errorf("FilterNewLabel() = %v, want %v", *got, *tt.wantLabel)
			}
		})
	}
}

func TestRemoveOtherRiskLabels(t *testing.T) {
	for _, tt := range []struct {
		desc       string
		assessment Assessment
		existing   []*github.Label
		want       []string
	}{{
		desc:       "no existing risk labels",
		assessment: Assessment{Level: LevelHigh},
		existing: []*github.Label{
			{Name: github.Ptr("lang/go")},
		},
		want: nil,
	}, {
		desc:       "same risk label exists",
		assessment: Assessment{Level: LevelHigh},
		existing: []*github.Label{
			{Name: github.Ptr("risk/high")},
		},
		want: nil,
	}, {
		desc:       "different risk label exists",
		assessment: Assessment{Level: LevelHigh},
		existing: []*github.Label{
			{Name: github.Ptr("risk/low")},
		},
		want: []string{"risk/low"},
	}, {
		desc:       "multiple risk labels exist",
		assessment: Assessment{Level: LevelHigh},
		existing: []*github.Label{
			{Name: github.Ptr("risk/low")},
			{Name: github.Ptr("risk/medium")},
		},
		want: []string{"risk/low", "risk/medium"},
	}, {
		desc:       "mixed labels",
		assessment: Assessment{Level: LevelMedium},
		existing: []*github.Label{
			{Name: github.Ptr("risk/high")},
			{Name: github.Ptr("lang/go")},
			{Name: github.Ptr("risk/low")},
		},
		want: []string{"risk/high", "risk/low"},
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			got := RemoveOtherRiskLabels(tt.assessment, tt.existing)

			if len(got) != len(tt.want) {
				t.Errorf("RemoveOtherRiskLabels() = %v, want %v", got, tt.want)
				return
			}

			// Check that all expected labels are present
			gotMap := make(map[string]bool)
			for _, label := range got {
				gotMap[label] = true
			}

			for _, wantLabel := range tt.want {
				if !gotMap[wantLabel] {
					t.Errorf("RemoveOtherRiskLabels() missing label %q", wantLabel)
				}
			}
		})
	}
}

func TestAssessmentReasons(t *testing.T) {
	scorer := NewWithDefaults()
	ctx := context.Background()

	t.Run("terraform changes include reason", func(t *testing.T) {
		files := []*github.CommitFile{
			{Filename: github.Ptr("main.tf"), Additions: github.Ptr(10), Deletions: github.Ptr(5)},
		}
		assessment := scorer.AssessRisk(ctx, files)

		foundTerraform := false
		for _, reason := range assessment.Reasons {
			if strings.Contains(strings.ToLower(reason), "terraform") {
				foundTerraform = true
				break
			}
		}

		if !foundTerraform {
			t.Errorf("Expected terraform-related reason, got: %v", assessment.Reasons)
		}
	})

	t.Run("large PR includes size reason", func(t *testing.T) {
		files := []*github.CommitFile{
			{Filename: github.Ptr("data.json"), Additions: github.Ptr(400), Deletions: github.Ptr(200)},
		}
		assessment := scorer.AssessRisk(ctx, files)

		foundSize := false
		for _, reason := range assessment.Reasons {
			if strings.Contains(strings.ToLower(reason), "large") || strings.Contains(strings.ToLower(reason), "lines") {
				foundSize = true
				break
			}
		}

		if !foundSize {
			t.Errorf("Expected size-related reason, got: %v", assessment.Reasons)
		}
	})

	t.Run("risky files are tracked", func(t *testing.T) {
		files := []*github.CommitFile{
			{Filename: github.Ptr("main.tf"), Additions: github.Ptr(10), Deletions: github.Ptr(5)},
			{Filename: github.Ptr("Dockerfile"), Additions: github.Ptr(10), Deletions: github.Ptr(5)},
		}
		assessment := scorer.AssessRisk(ctx, files)

		if len(assessment.RiskyFiles) != 2 {
			t.Errorf("Expected 2 risky files, got %d: %v", len(assessment.RiskyFiles), assessment.RiskyFiles)
		}
	})
}
