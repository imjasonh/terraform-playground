package mta

import (
	"context"
	"strings"
	"testing"
)

func TestClientFetchAlertsFixture(t *testing.T) {
	client := NewClient("file://" + testdataPath(t, "sample-alerts.json"))
	feed, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Entity) == 0 {
		t.Fatal("expected alert entities")
	}
}

func TestClientFetchMissingFile(t *testing.T) {
	client := NewClient("file:///no/such/file.json")
	_, err := client.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "open fixture") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewClientDefaults(t *testing.T) {
	client := NewClient("")
	if client.URL != DefaultFeedURL {
		t.Fatalf("default url: got %q", client.URL)
	}
}
