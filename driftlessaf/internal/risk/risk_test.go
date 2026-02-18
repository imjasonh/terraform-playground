package risk

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Model == "" {
		t.Error("default model should not be empty")
	}

	if cfg.MaxTokens == 0 {
		t.Error("default max tokens should not be zero")
	}

	if cfg.Temperature < 0 || cfg.Temperature > 1 {
		t.Errorf("default temperature should be between 0 and 1, got %f", cfg.Temperature)
	}
}

func TestRiskLevels(t *testing.T) {
	levels := []Level{LevelLow, LevelMedium, LevelHigh}
	expected := []string{"low", "medium", "high"}

	for i, level := range levels {
		if string(level) != expected[i] {
			t.Errorf("level %d: got %s, want %s", i, level, expected[i])
		}
	}
}

func TestPRContextBind(t *testing.T) {
	ctx := &PRContext{
		Owner:        "test",
		Repo:         "repo",
		PRNumber:     1,
		Title:        "Test PR",
		FilesChanged: []string{"file1.go", "file2.go"},
		Additions:    10,
		Deletions:    5,
	}

	// Just verify it doesn't panic
	if ctx.Owner != "test" {
		t.Error("context not initialized properly")
	}
}

func TestFormatFileDiffs(t *testing.T) {
	diffs := map[string]string{
		"file1.go": "diff content 1",
		"file2.go": "diff content 2",
	}

	result := formatFileDiffs(diffs)

	if result == "" {
		t.Error("formatted diffs should not be empty")
	}

	// Check that both files are included
	if !(contains(result, "file1.go") && contains(result, "file2.go")) {
		t.Error("formatted diffs should include all filenames")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || (len(s) > len(substr) && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
