package main

import (
	"testing"
)

func TestParsePRURL(t *testing.T) {
	for _, tt := range []struct {
		desc       string
		url        string
		wantOwner  string
		wantRepo   string
		wantNumber int
		wantErr    bool
	}{{
		desc:       "valid PR URL",
		url:        "https://github.com/owner/repo/pull/123",
		wantOwner:  "owner",
		wantRepo:   "repo",
		wantNumber: 123,
		wantErr:    false,
	}, {
		desc:       "another valid PR URL",
		url:        "https://github.com/imjasonh/terraform-playground/pull/456",
		wantOwner:  "imjasonh",
		wantRepo:   "terraform-playground",
		wantNumber: 456,
		wantErr:    false,
	}, {
		desc:    "invalid URL - not a PR",
		url:     "https://github.com/owner/repo/issues/123",
		wantErr: true,
	}, {
		desc:    "invalid URL - missing parts",
		url:     "https://github.com/owner/repo",
		wantErr: true,
	}, {
		desc:    "invalid URL - wrong domain",
		url:     "https://gitlab.com/owner/repo/pull/123",
		wantErr: true,
	}} {
		t.Run(tt.desc, func(t *testing.T) {
			owner, repo, number, err := parsePRURL(tt.url)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parsePRURL(%q) expected error, got nil", tt.url)
				}
				return
			}

			if err != nil {
				t.Fatalf("parsePRURL(%q) unexpected error: %v", tt.url, err)
			}

			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
			if number != tt.wantNumber {
				t.Errorf("number = %d, want %d", number, tt.wantNumber)
			}
		})
	}
}

func TestReconcilerDetails_Markdown(t *testing.T) {
	tests := []struct {
		desc    string
		details ReconcilerDetails
		want    []string // Substrings that should be present
	}{{
		desc: "high risk",
		details: ReconcilerDetails{
			RiskLevel:     "high",
			FilesAnalyzed: 5,
			TotalChanges:  200,
			Reasons:       []string{"Large changes", "Critical files modified"},
			RiskyFiles:    []string{"main.tf", "Dockerfile"},
		},
		want: []string{"Risk Assessment", "HIGH", "5 files", "200 lines", "Large changes", "main.tf"},
	}, {
		desc: "medium risk",
		details: ReconcilerDetails{
			RiskLevel:     "medium",
			FilesAnalyzed: 3,
			TotalChanges:  100,
			Reasons:       []string{"Code changes require review"},
		},
		want: []string{"Risk Assessment", "MEDIUM", "3 files", "100 lines"},
	}, {
		desc: "low risk",
		details: ReconcilerDetails{
			RiskLevel:     "low",
			FilesAnalyzed: 2,
			TotalChanges:  20,
			Reasons:       []string{"Documentation only"},
		},
		want: []string{"Risk Assessment", "LOW", "2 files", "20 lines"},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			md := tt.details.Markdown()

			for _, substr := range tt.want {
				if !containsSubstring(md, substr) {
					t.Errorf("Markdown() missing expected substring %q\nGot:\n%s", substr, md)
				}
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	// Case-insensitive check for simplicity
	s = toLower(s)
	substr = toLower(substr)
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + ('a' - 'A')
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
