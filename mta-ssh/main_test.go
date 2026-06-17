package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindFixture(t *testing.T) {
	path := findFixture("testdata/sample-alerts.json")
	if path == "" {
		t.Fatal("expected to find sample-alerts.json")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture path invalid: %v", err)
	}
}

func TestDefaultRefreshSec(t *testing.T) {
	t.Setenv("MTA_REFRESH_SEC", "7")
	if got := defaultRefreshSec(); got != 7 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("MTA_REFRESH_SEC", "bad")
	if got := defaultRefreshSec(); got != 10 {
		t.Fatalf("invalid env should fall back, got %d", got)
	}
}

func TestDefaultAlertsFixtureUsesFileWhenPresent(t *testing.T) {
	if _, err := os.Stat(filepath.Join("testdata", "sample-alerts.json")); err != nil {
		t.Skip("testdata not in cwd")
	}
	url := defaultAlertsFixture()
	if url == "" || url[:7] != "file://" {
		t.Fatalf("got %q", url)
	}
}
