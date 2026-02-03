// Package autolabeler provides functionality for automatically labeling
// GitHub pull requests based on changed files and other criteria.
package autolabeler

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-github/v75/github"
)

// Rule defines a rule for auto-labeling based on file patterns.
type Rule struct {
	Label    string   // Label to apply
	Patterns []string // Glob patterns to match (e.g., "*.go", "src/frontend/**")
}

// SizeThreshold defines a threshold for size-based labels.
type SizeThreshold struct {
	Label    string
	MaxLines int // -1 means unlimited (matches anything above previous thresholds)
}

// Config holds the configuration for the auto-labeler.
type Config struct {
	Rules           []Rule
	SizeThresholds  []SizeThreshold
}

// DefaultConfig returns the default auto-labeler configuration.
func DefaultConfig() Config {
	return Config{
		Rules: []Rule{
			// Language/Technology labels
			{Label: "lang/go", Patterns: []string{"*.go", "go.mod", "go.sum"}},
			{Label: "lang/typescript", Patterns: []string{"*.ts", "*.tsx"}},
			{Label: "lang/javascript", Patterns: []string{"*.js", "*.jsx"}},
			{Label: "lang/python", Patterns: []string{"*.py", "requirements.txt", "pyproject.toml"}},
			{Label: "lang/rust", Patterns: []string{"*.rs", "Cargo.toml", "Cargo.lock"}},
			{Label: "lang/terraform", Patterns: []string{"*.tf", "*.tfvars"}},

			// Area labels
			{Label: "area/docs", Patterns: []string{"*.md", "docs/**", "README*"}},
			{Label: "area/ci", Patterns: []string{".github/**", ".gitlab-ci.yml", "Jenkinsfile"}},
			{Label: "area/docker", Patterns: []string{"Dockerfile*", "docker-compose*", ".dockerignore"}},
			{Label: "area/k8s", Patterns: []string{"*.yaml", "*.yml", "k8s/**", "kubernetes/**", "helm/**"}},
			{Label: "area/tests", Patterns: []string{"*_test.go", "**/*_test.go", "test/**", "tests/**", "**/*.test.ts", "**/*.spec.ts"}},
		},
		SizeThresholds: []SizeThreshold{
			{Label: "size/XS", MaxLines: 10},
			{Label: "size/S", MaxLines: 50},
			{Label: "size/M", MaxLines: 200},
			{Label: "size/L", MaxLines: 500},
			{Label: "size/XL", MaxLines: -1}, // unlimited
		},
	}
}

// Labeler calculates labels for pull requests based on changed files.
type Labeler struct {
	config Config
}

// New creates a new Labeler with the given configuration.
func New(config Config) *Labeler {
	return &Labeler{config: config}
}

// NewWithDefaults creates a new Labeler with the default configuration.
func NewWithDefaults() *Labeler {
	return New(DefaultConfig())
}

// Result contains the results of label calculation.
type Result struct {
	Labels        []string
	FilesAnalyzed int
	TotalChanges  int
}

// CalculateLabels determines which labels to apply based on the changed files.
func (l *Labeler) CalculateLabels(files []*github.CommitFile) Result {
	labelSet := make(map[string]bool)

	// Calculate total lines changed for size label
	totalChanges := 0
	for _, f := range files {
		totalChanges += f.GetAdditions() + f.GetDeletions()
	}

	// Apply size label
	for _, threshold := range l.config.SizeThresholds {
		if threshold.MaxLines == -1 || totalChanges <= threshold.MaxLines {
			labelSet[threshold.Label] = true
			break
		}
	}

	// Check each file against label rules
	for _, file := range files {
		filename := file.GetFilename()

		for _, rule := range l.config.Rules {
			if l.fileMatchesRule(filename, rule) {
				labelSet[rule.Label] = true
			}
		}
	}

	// Convert to sorted slice
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	return Result{
		Labels:        labels,
		FilesAnalyzed: len(files),
		TotalChanges:  totalChanges,
	}
}

// CalculateLabelsFromFilenames is a convenience method that calculates labels
// from a list of filenames with optional change counts.
func (l *Labeler) CalculateLabelsFromFilenames(filenames []string, additions, deletions int) Result {
	files := make([]*github.CommitFile, len(filenames))
	for i, name := range filenames {
		files[i] = &github.CommitFile{
			Filename:  github.Ptr(name),
			Additions: github.Ptr(additions / len(filenames)),
			Deletions: github.Ptr(deletions / len(filenames)),
		}
	}
	return l.CalculateLabels(files)
}

// fileMatchesRule checks if a filename matches any pattern in a rule.
func (l *Labeler) fileMatchesRule(filename string, rule Rule) bool {
	for _, pattern := range rule.Patterns {
		if matched, _ := MatchPattern(pattern, filename); matched {
			return true
		}
	}
	return false
}

// MatchPattern checks if a filename matches a glob pattern.
// Supports ** for recursive matching.
func MatchPattern(pattern, filename string) (bool, error) {
	// Handle ** patterns by converting to regex
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPattern(pattern, filename)
	}

	// Use filepath.Match for simple patterns
	// But we need to match against just the filename for patterns like "*.go"
	if !strings.Contains(pattern, "/") {
		// Pattern doesn't contain path separator, match against basename
		return filepath.Match(pattern, filepath.Base(filename))
	}

	return filepath.Match(pattern, filename)
}

// matchDoubleStarPattern handles patterns containing ** by converting to regex.
func matchDoubleStarPattern(pattern, filename string) (bool, error) {
	// Convert glob to regex
	regexPattern := "^" + regexp.QuoteMeta(pattern) + "$"
	regexPattern = strings.ReplaceAll(regexPattern, `\*\*`, ".*")
	regexPattern = strings.ReplaceAll(regexPattern, `\*`, "[^/]*")

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(filename), nil
}

// FilterNewLabels returns only labels that are not already present on the PR.
func FilterNewLabels(calculated []string, existing []*github.Label) []string {
	existingSet := make(map[string]bool)
	for _, label := range existing {
		existingSet[label.GetName()] = true
	}

	var newLabels []string
	for _, label := range calculated {
		if !existingSet[label] {
			newLabels = append(newLabels, label)
		}
	}
	return newLabels
}
