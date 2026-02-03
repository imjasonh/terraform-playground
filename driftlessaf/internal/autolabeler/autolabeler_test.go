package autolabeler

import (
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
		want:     true, // Matches because we check basename for patterns without /
	}, {
		desc:     "prefix wildcard match",
		pattern:  "Dockerfile*",
		filename: "Dockerfile",
		want:     true,
	}, {
		desc:     "prefix wildcard with suffix",
		pattern:  "Dockerfile*",
		filename: "Dockerfile.prod",
		want:     true,
	}, {
		desc:     "prefix wildcard no match",
		pattern:  "Dockerfile*",
		filename: "docker-compose.yml",
		want:     false,
	}, {
		desc:     "README prefix",
		pattern:  "README*",
		filename: "README.md",
		want:     true,
	}, {
		desc:     "double star recursive match",
		pattern:  "docs/**",
		filename: "docs/guide.md",
		want:     true,
	}, {
		desc:     "double star deep recursive",
		pattern:  "docs/**",
		filename: "docs/api/v1/spec.md",
		want:     true,
	}, {
		desc:     "double star no match outside",
		pattern:  "docs/**",
		filename: "src/docs/file.md",
		want:     false,
	}, {
		desc:     "double star with extension",
		pattern:  "**/*.test.ts",
		filename: "src/components/Button.test.ts",
		want:     true,
	}, {
		desc:     "double star test file deep",
		pattern:  "**/*_test.go",
		filename: "pkg/foo/bar/baz_test.go",
		want:     true,
	}, {
		desc:     "github actions directory",
		pattern:  ".github/**",
		filename: ".github/workflows/ci.yml",
		want:     true,
	}, {
		desc:     "test directory",
		pattern:  "test/**",
		filename: "test/integration/foo_test.go",
		want:     true,
	}, {
		desc:     "k8s yaml files",
		pattern:  "k8s/**",
		filename: "k8s/deployment.yaml",
		want:     true,
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := MatchPattern(tt.pattern, tt.filename)
			if err != nil {
				t.Fatalf("MatchPattern(%q, %q) error: %v", tt.pattern, tt.filename, err)
			}
			if got != tt.want {
				t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.filename, got, tt.want)
			}
		})
	}
}

func TestLabeler_CalculateLabels(t *testing.T) {
	labeler := NewWithDefaults()

	for _, tt := range []struct {
		desc       string
		files      []*github.CommitFile
		wantLabels []string
	}{{
		desc: "single go file",
		files: []*github.CommitFile{
			{Filename: github.Ptr("main.go"), Additions: github.Ptr(5), Deletions: github.Ptr(0)},
		},
		wantLabels: []string{"lang/go", "size/XS"},
	}, {
		desc: "go mod files",
		files: []*github.CommitFile{
			{Filename: github.Ptr("go.mod"), Additions: github.Ptr(2), Deletions: github.Ptr(1)},
			{Filename: github.Ptr("go.sum"), Additions: github.Ptr(10), Deletions: github.Ptr(5)},
		},
		wantLabels: []string{"lang/go", "size/S"},
	}, {
		desc: "typescript files",
		files: []*github.CommitFile{
			{Filename: github.Ptr("src/App.tsx"), Additions: github.Ptr(20), Deletions: github.Ptr(10)},
			{Filename: github.Ptr("src/utils.ts"), Additions: github.Ptr(15), Deletions: github.Ptr(5)},
		},
		wantLabels: []string{"lang/typescript", "size/S"}, // 50 lines total = size/S
	}, {
		desc: "mixed languages",
		files: []*github.CommitFile{
			{Filename: github.Ptr("main.go"), Additions: github.Ptr(10), Deletions: github.Ptr(5)},
			{Filename: github.Ptr("script.py"), Additions: github.Ptr(8), Deletions: github.Ptr(2)},
		},
		wantLabels: []string{"lang/go", "lang/python", "size/S"},
	}, {
		desc: "terraform files",
		files: []*github.CommitFile{
			{Filename: github.Ptr("main.tf"), Additions: github.Ptr(50), Deletions: github.Ptr(20)},
			{Filename: github.Ptr("variables.tf"), Additions: github.Ptr(30), Deletions: github.Ptr(10)},
			{Filename: github.Ptr("terraform.tfvars"), Additions: github.Ptr(5), Deletions: github.Ptr(0)},
		},
		wantLabels: []string{"lang/terraform", "size/M"},
	}, {
		desc: "documentation",
		files: []*github.CommitFile{
			{Filename: github.Ptr("README.md"), Additions: github.Ptr(20), Deletions: github.Ptr(5)},
			{Filename: github.Ptr("docs/guide.md"), Additions: github.Ptr(100), Deletions: github.Ptr(0)},
		},
		wantLabels: []string{"area/docs", "size/M"},
	}, {
		desc: "ci configuration",
		files: []*github.CommitFile{
			{Filename: github.Ptr(".github/workflows/ci.yml"), Additions: github.Ptr(30), Deletions: github.Ptr(10)},
		},
		wantLabels: []string{"area/ci", "area/k8s", "size/S"},
	}, {
		desc: "docker files",
		files: []*github.CommitFile{
			{Filename: github.Ptr("Dockerfile"), Additions: github.Ptr(15), Deletions: github.Ptr(5)},
			{Filename: github.Ptr("docker-compose.yml"), Additions: github.Ptr(20), Deletions: github.Ptr(0)},
		},
		wantLabels: []string{"area/docker", "area/k8s", "size/S"},
	}, {
		desc: "test files",
		files: []*github.CommitFile{
			{Filename: github.Ptr("pkg/foo/foo_test.go"), Additions: github.Ptr(50), Deletions: github.Ptr(10)},
		},
		wantLabels: []string{"area/tests", "lang/go", "size/M"},
	}, {
		desc: "kubernetes manifests",
		files: []*github.CommitFile{
			{Filename: github.Ptr("k8s/deployment.yaml"), Additions: github.Ptr(40), Deletions: github.Ptr(0)},
			{Filename: github.Ptr("k8s/service.yaml"), Additions: github.Ptr(20), Deletions: github.Ptr(0)},
		},
		wantLabels: []string{"area/k8s", "size/M"},
	}, {
		desc: "large change",
		files: []*github.CommitFile{
			{Filename: github.Ptr("big_refactor.go"), Additions: github.Ptr(400), Deletions: github.Ptr(200)},
		},
		wantLabels: []string{"lang/go", "size/XL"},
	}, {
		desc: "rust project",
		files: []*github.CommitFile{
			{Filename: github.Ptr("src/main.rs"), Additions: github.Ptr(30), Deletions: github.Ptr(10)},
			{Filename: github.Ptr("Cargo.toml"), Additions: github.Ptr(5), Deletions: github.Ptr(2)},
		},
		wantLabels: []string{"lang/rust", "size/S"},
	}, {
		desc: "javascript and test",
		files: []*github.CommitFile{
			{Filename: github.Ptr("src/app.js"), Additions: github.Ptr(25), Deletions: github.Ptr(5)},
			{Filename: github.Ptr("test/app.test.js"), Additions: github.Ptr(40), Deletions: github.Ptr(10)},
		},
		wantLabels: []string{"area/tests", "lang/javascript", "size/M"},
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			result := labeler.CalculateLabels(tt.files)

			if len(result.Labels) != len(tt.wantLabels) {
				t.Errorf("got %d labels %v, want %d labels %v",
					len(result.Labels), result.Labels,
					len(tt.wantLabels), tt.wantLabels)
				return
			}

			for i, got := range result.Labels {
				if got != tt.wantLabels[i] {
					t.Errorf("label[%d] = %q, want %q", i, got, tt.wantLabels[i])
				}
			}
		})
	}
}

func TestSizeThresholds(t *testing.T) {
	labeler := NewWithDefaults()

	for _, tt := range []struct {
		desc      string
		additions int
		deletions int
		wantSize  string
	}{{
		desc:      "XS: 1 line",
		additions: 1,
		deletions: 0,
		wantSize:  "size/XS",
	}, {
		desc:      "XS: 10 lines",
		additions: 5,
		deletions: 5,
		wantSize:  "size/XS",
	}, {
		desc:      "S: 11 lines",
		additions: 6,
		deletions: 5,
		wantSize:  "size/S",
	}, {
		desc:      "S: 50 lines",
		additions: 30,
		deletions: 20,
		wantSize:  "size/S",
	}, {
		desc:      "M: 51 lines",
		additions: 30,
		deletions: 21,
		wantSize:  "size/M",
	}, {
		desc:      "M: 200 lines",
		additions: 100,
		deletions: 100,
		wantSize:  "size/M",
	}, {
		desc:      "L: 201 lines",
		additions: 101,
		deletions: 100,
		wantSize:  "size/L",
	}, {
		desc:      "L: 500 lines",
		additions: 300,
		deletions: 200,
		wantSize:  "size/L",
	}, {
		desc:      "XL: 501 lines",
		additions: 301,
		deletions: 200,
		wantSize:  "size/XL",
	}, {
		desc:      "XL: 1000 lines",
		additions: 600,
		deletions: 400,
		wantSize:  "size/XL",
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			files := []*github.CommitFile{
				{
					Filename:  github.Ptr("file.txt"),
					Additions: github.Ptr(tt.additions),
					Deletions: github.Ptr(tt.deletions),
				},
			}

			result := labeler.CalculateLabels(files)

			var gotSize string
			for _, label := range result.Labels {
				if len(label) > 5 && label[:5] == "size/" {
					gotSize = label
					break
				}
			}

			if gotSize != tt.wantSize {
				t.Errorf("got size label %q, want %q (changes: %d)",
					gotSize, tt.wantSize, tt.additions+tt.deletions)
			}
		})
	}
}

func TestFilterNewLabels(t *testing.T) {
	for _, tt := range []struct {
		desc       string
		calculated []string
		existing   []*github.Label
		want       []string
	}{{
		desc:       "no existing labels",
		calculated: []string{"lang/go", "size/XS"},
		existing:   nil,
		want:       []string{"lang/go", "size/XS"},
	}, {
		desc:       "all labels exist",
		calculated: []string{"lang/go", "size/XS"},
		existing: []*github.Label{
			{Name: github.Ptr("lang/go")},
			{Name: github.Ptr("size/XS")},
		},
		want: nil,
	}, {
		desc:       "some labels exist",
		calculated: []string{"lang/go", "lang/python", "size/S"},
		existing: []*github.Label{
			{Name: github.Ptr("lang/go")},
		},
		want: []string{"lang/python", "size/S"},
	}, {
		desc:       "existing has extra labels",
		calculated: []string{"lang/go"},
		existing: []*github.Label{
			{Name: github.Ptr("lang/go")},
			{Name: github.Ptr("needs-review")},
			{Name: github.Ptr("priority/high")},
		},
		want: nil,
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			got := FilterNewLabels(tt.calculated, tt.existing)

			if len(got) != len(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
				return
			}

			for i, label := range got {
				if label != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, label, tt.want[i])
				}
			}
		})
	}
}

func TestCustomConfig(t *testing.T) {
	config := Config{
		Rules: []Rule{
			{Label: "custom/frontend", Patterns: []string{"src/ui/**", "*.css"}},
			{Label: "custom/backend", Patterns: []string{"src/api/**", "*.go"}},
		},
		SizeThresholds: []SizeThreshold{
			{Label: "tiny", MaxLines: 5},
			{Label: "small", MaxLines: 20},
			{Label: "big", MaxLines: -1},
		},
	}

	labeler := New(config)

	files := []*github.CommitFile{
		{Filename: github.Ptr("src/ui/Button.css"), Additions: github.Ptr(10), Deletions: github.Ptr(2)},
	}

	result := labeler.CalculateLabels(files)

	wantLabels := []string{"custom/frontend", "small"}
	if len(result.Labels) != len(wantLabels) {
		t.Errorf("got %v, want %v", result.Labels, wantLabels)
		return
	}

	for i, got := range result.Labels {
		if got != wantLabels[i] {
			t.Errorf("label[%d] = %q, want %q", i, got, wantLabels[i])
		}
	}
}
