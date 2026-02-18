// Package risk provides functionality for assessing the risk level of
// GitHub pull requests based on changed files, size, and content analysis.
package risk

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/go-github/v75/github"
)

// Level represents the risk level of a PR.
type Level string

const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
)

// Label returns the GitHub label name for this risk level.
func (l Level) Label() string {
	return fmt.Sprintf("risk/%s", l)
}

// Priority returns a numeric priority for comparison (higher = more risky).
func (l Level) Priority() int {
	switch l {
	case LevelHigh:
		return 3
	case LevelMedium:
		return 2
	case LevelLow:
		return 1
	default:
		return 0
	}
}

// Rule defines a rule for risk assessment based on file patterns.
type Rule struct {
	Level       Level
	Patterns    []string
	Description string
}

// Config holds the configuration for risk assessment.
type Config struct {
	Rules []Rule
	// SizeThresholds define risk levels based on PR size
	LargePRThreshold   int // Lines changed that triggers high risk
	MediumPRThreshold  int // Lines changed that triggers medium risk
}

// DefaultConfig returns the default risk assessment configuration.
func DefaultConfig() Config {
	return Config{
		Rules: []Rule{
			// High risk: Critical infrastructure files
			{
				Level:       LevelHigh,
				Patterns:    []string{"*.tf", "*.tfvars", "terraform.tfstate*"},
				Description: "Terraform infrastructure changes can affect cost, stability, and availability",
			},
			{
				Level:       LevelHigh,
				Patterns:    []string{"Dockerfile*", "docker-compose*"},
				Description: "Container configuration changes can affect deployment and security",
			},
			{
				Level:       LevelHigh,
				Patterns:    []string{"k8s/**/*.yaml", "k8s/**/*.yml", "kubernetes/**/*.yaml", "kubernetes/**/*.yml", "helm/**/*.yaml", "helm/**/*.yml"},
				Description: "Kubernetes configuration changes can affect service availability",
			},
			{
				Level:       LevelHigh,
				Patterns:    []string{".github/workflows/**"},
				Description: "CI/CD workflow changes can affect build and deployment processes",
			},
			{
				Level:       LevelHigh,
				Patterns:    []string{"go.mod", "package.json", "requirements.txt", "Cargo.toml"},
				Description: "Dependency changes can introduce security vulnerabilities or breaking changes",
			},
			{
				Level:       LevelHigh,
				Patterns:    []string{"**/migrations/**", "**/*migration*"},
				Description: "Database migrations can affect data integrity and require careful review",
			},

			// Medium risk: Important but less critical changes
			{
				Level:       LevelMedium,
				Patterns:    []string{"*.go", "*.rs", "*.java", "*.cpp", "*.c"},
				Description: "Backend code changes require review for logic and performance",
			},
			{
				Level:       LevelMedium,
				Patterns:    []string{"*.ts", "*.tsx", "*.js", "*.jsx"},
				Description: "Frontend code changes can affect user experience",
			},
			{
				Level:       LevelMedium,
				Patterns:    []string{"*.py"},
				Description: "Python code changes require review",
			},
			{
				Level:       LevelMedium,
				Patterns:    []string{"**/config/**", "*.conf", "*.config", "*.toml", "*.ini"},
				Description: "Configuration changes can affect application behavior",
			},
		},
		LargePRThreshold:  500,  // 500+ lines is high risk
		MediumPRThreshold: 200,  // 200+ lines is medium risk
	}
}

// Scorer assesses risk for pull requests.
type Scorer struct {
	config Config
}

// New creates a new Scorer with the given configuration.
func New(config Config) *Scorer {
	return &Scorer{config: config}
}

// NewWithDefaults creates a new Scorer with the default configuration.
func NewWithDefaults() *Scorer {
	return New(DefaultConfig())
}

// Assessment contains the result of risk assessment.
type Assessment struct {
	Level         Level
	FilesAnalyzed int
	TotalChanges  int
	Reasons       []string
	RiskyFiles    []string
}

// AssessRisk determines the risk level based on the changed files.
func (s *Scorer) AssessRisk(ctx context.Context, files []*github.CommitFile) Assessment {
	// Calculate total lines changed
	totalChanges := 0
	for _, f := range files {
		totalChanges += f.GetAdditions() + f.GetDeletions()
	}

	assessment := Assessment{
		Level:         LevelLow,
		FilesAnalyzed: len(files),
		TotalChanges:  totalChanges,
		Reasons:       []string{},
		RiskyFiles:    []string{},
	}

	// Check size-based risk
	if totalChanges >= s.config.LargePRThreshold {
		assessment.Level = LevelHigh
		assessment.Reasons = append(assessment.Reasons, 
			fmt.Sprintf("Large PR with %d lines changed (threshold: %d)", totalChanges, s.config.LargePRThreshold))
	} else if totalChanges >= s.config.MediumPRThreshold {
		if assessment.Level.Priority() < LevelMedium.Priority() {
			assessment.Level = LevelMedium
		}
		assessment.Reasons = append(assessment.Reasons, 
			fmt.Sprintf("Medium-sized PR with %d lines changed (threshold: %d)", totalChanges, s.config.MediumPRThreshold))
	}

	// Track highest risk level found in files
	fileRiskReasons := make(map[string][]string)
	
	// Check each file against risk rules
	for _, file := range files {
		filename := file.GetFilename()
		fileRiskLevel := LevelLow
		
		for _, rule := range s.config.Rules {
			if s.fileMatchesRule(filename, rule) {
				if rule.Level.Priority() > fileRiskLevel.Priority() {
					fileRiskLevel = rule.Level
				}
				// Track reason for this file
				if rule.Level.Priority() >= LevelMedium.Priority() {
					fileRiskReasons[filename] = append(fileRiskReasons[filename], rule.Description)
				}
			}
		}
		
		// Add to risky files if medium or high risk
		if fileRiskLevel.Priority() >= LevelMedium.Priority() {
			assessment.RiskyFiles = append(assessment.RiskyFiles, filename)
		}
		
		// Update overall risk level
		if fileRiskLevel.Priority() > assessment.Level.Priority() {
			assessment.Level = fileRiskLevel
		}
	}

	// Remove duplicates from risky files
	assessment.RiskyFiles = uniqueStrings(assessment.RiskyFiles)
	sort.Strings(assessment.RiskyFiles)

	// Add file-based reasons
	reasonMap := make(map[string]bool)
	for _, reasons := range fileRiskReasons {
		for _, reason := range reasons {
			if !reasonMap[reason] {
				reasonMap[reason] = true
				assessment.Reasons = append(assessment.Reasons, reason)
			}
		}
	}

	// If no specific reasons, add a default
	if len(assessment.Reasons) == 0 {
		if len(files) == 0 {
			assessment.Reasons = append(assessment.Reasons, "No files changed")
		} else {
			assessment.Reasons = append(assessment.Reasons, "Changes do not match high-risk patterns")
		}
	}

	return assessment
}

// fileMatchesRule checks if a filename matches any pattern in a rule.
func (s *Scorer) fileMatchesRule(filename string, rule Rule) bool {
	for _, pattern := range rule.Patterns {
		if matched, _ := matchPattern(pattern, filename); matched {
			return true
		}
	}
	return false
}

// matchPattern checks if a filename matches a glob pattern.
// Supports ** for recursive matching.
func matchPattern(pattern, filename string) (bool, error) {
	// Handle ** patterns
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPattern(pattern, filename)
	}

	// Use filepath.Match for simple patterns
	if !strings.Contains(pattern, "/") {
		// Pattern doesn't contain path separator, match against basename
		return filepath.Match(pattern, filepath.Base(filename))
	}

	return filepath.Match(pattern, filename)
}

// matchDoubleStarPattern handles patterns containing ** by converting to simple matching.
func matchDoubleStarPattern(pattern, filename string) (bool, error) {
	// Handle patterns like **/migrations/** or **/*migration*
	
	// Replace ** with a placeholder and then check
	parts := strings.Split(pattern, "**")
	
	// For patterns like **/migrations/**
	// We need to check if any path component matches
	if len(parts) == 2 {
		prefix := parts[0]
		suffix := parts[1]
		
		// Simple case: prefix/**
		if suffix == "" || suffix == "/" {
			return strings.HasPrefix(filename, prefix), nil
		}
		
		// Simple case: **/suffix
		if prefix == "" {
			if strings.HasPrefix(suffix, "/") {
				suffix = suffix[1:]
			}
			if suffix == "" {
				return true, nil
			}
			// Check if the filename contains the suffix as a path component or file pattern
			if strings.Contains(filename, suffix) {
				return true, nil
			}
			matched, err := filepath.Match(suffix, filepath.Base(filename))
			if err != nil {
				return false, err
			}
			return matched, nil
		}
		
		// Complex case: prefix/**/suffix (e.g., "k8s/**" or ".github/workflows/**")
		if !strings.HasPrefix(filename, prefix) {
			return false, nil
		}
		
		if suffix == "" || suffix == "/" {
			return true, nil
		}
		
		// Remove prefix and check the rest
		remaining := filename[len(prefix):]
		if strings.HasPrefix(suffix, "/") {
			suffix = suffix[1:]
		}
		
		if suffix == "" {
			return true, nil
		}
		
		// Check if suffix is present in remaining path
		if strings.Contains(remaining, suffix) {
			return true, nil
		}
		
		// Try glob match on the basename
		matched, err := filepath.Match(suffix, filepath.Base(remaining))
		if err != nil {
			return false, err
		}
		return matched, nil
	}
	
	// For patterns with multiple **, like **/migrations/**
	// Check if all non-** parts are present in the path in order
	if len(parts) == 3 {
		// Pattern like **/migrations/**
		prefix := parts[0]
		middle := parts[1]
		suffix := parts[2]
		
		// Check if middle part exists in the filename
		if middle != "" && middle != "/" {
			// Remove leading/trailing slashes from middle
			middle = strings.Trim(middle, "/")
			if !strings.Contains(filename, middle) {
				return false, nil
			}
			
			// If there's a prefix, check it
			if prefix != "" && !strings.HasPrefix(filename, prefix) {
				return false, nil
			}
			
			// If there's a suffix, check it appears after middle
			if suffix != "" && suffix != "/" {
				suffix = strings.Trim(suffix, "/")
				middleIdx := strings.Index(filename, middle)
				if middleIdx >= 0 {
					afterMiddle := filename[middleIdx+len(middle):]
					if suffix != "" && !strings.Contains(afterMiddle, suffix) {
						// Also try as a glob pattern
						matched, err := filepath.Match(suffix, filepath.Base(afterMiddle))
						if err != nil || !matched {
							return false, err
						}
					}
				}
			}
			
			return true, nil
		}
	}
	
	return false, nil
}

// FilterNewLabel returns the risk label if it's not already present on the PR.
func FilterNewLabel(assessment Assessment, existing []*github.Label) *string {
	existingSet := make(map[string]bool)
	for _, label := range existing {
		existingSet[label.GetName()] = true
	}

	label := assessment.Level.Label()
	if !existingSet[label] {
		return &label
	}
	return nil
}

// RemoveOtherRiskLabels returns a list of risk labels that should be removed
// based on the current assessment.
func RemoveOtherRiskLabels(assessment Assessment, existing []*github.Label) []string {
	currentLabel := assessment.Level.Label()
	var toRemove []string
	
	for _, label := range existing {
		name := label.GetName()
		if strings.HasPrefix(name, "risk/") && name != currentLabel {
			toRemove = append(toRemove, name)
		}
	}
	
	return toRemove
}

func uniqueStrings(slice []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, str := range slice {
		if !seen[str] {
			seen[str] = true
			result = append(result, str)
		}
	}
	return result
}
