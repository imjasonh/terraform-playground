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
			RiskLevel:   "high",
			Reasoning:   "Infrastructure changes detected",
			RiskyFiles:  []string{"main.tf", "Dockerfile"},
			RiskFactors: []string{"Terraform changes", "Docker config"},
			Confidence:  0.95,
		},
		want: []string{"Risk Assessment", "HIGH", "Infrastructure changes", "main.tf"},
	}, {
		desc: "medium risk",
		details: ReconcilerDetails{
			RiskLevel:  "medium",
			Reasoning:  "API changes require review",
			Confidence: 0.85,
		},
		want: []string{"Risk Assessment", "MEDIUM", "API changes"},
	}, {
		desc: "low risk",
		details: ReconcilerDetails{
			RiskLevel:  "low",
			Reasoning:  "Documentation only",
			Confidence: 0.99,
		},
		want: []string{"Risk Assessment", "LOW", "Documentation"},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			md := tt.details.Markdown()

			for _, substr := range tt.want {
				if !contains(md, substr) {
					t.Errorf("Markdown() missing expected substring %q\nGot:\n%s", substr, md)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
