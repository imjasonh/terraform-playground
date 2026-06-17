package mta

import (
	"os"
	"path/filepath"
	"testing"
)

func testdataPath(t *testing.T, parts ...string) string {
	t.Helper()
	candidates := []string{
		filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...),
		filepath.Join(append([]string{"testdata"}, parts...)...),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	t.Fatalf("testdata not found: %v", parts)
	return ""
}
