package mta

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestLineStatusesFromFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/sample-alerts.json")
	if err != nil {
		t.Fatal(err)
	}

	var feed Feed
	if err := json.Unmarshal(data, &feed); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1734451200, 0)
	statuses := LineStatuses(feed, now)
	byRoute := make(map[string]string, len(statuses))
	for _, s := range statuses {
		byRoute[s.RouteID] = s.Status
	}

	if got := byRoute["4"]; got != "Delays" {
		t.Fatalf("route 4: got %q want Delays", got)
	}
	if got := byRoute["N"]; got != "Part Suspended" {
		t.Fatalf("route N: got %q want Part Suspended", got)
	}
	if got := byRoute["L"]; got != "Planned - Stops Skipped" {
		t.Fatalf("route L: got %q want Planned - Stops Skipped", got)
	}
	if got := byRoute["A"]; got != "Slow Speeds" {
		t.Fatalf("route A: got %q want Slow Speeds", got)
	}
	if got := byRoute["1"]; got != GoodService {
		t.Fatalf("route 1: got %q want %q", got, GoodService)
	}
}

func TestParseSortOrder(t *testing.T) {
	if got := parseSortOrder("MTASBWY:D:26"); got != 26 {
		t.Fatalf("got %d want 26", got)
	}
}
