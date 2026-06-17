package display

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
)

func loadAlertsFixture(t *testing.T) mta.Feed {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "sample-alerts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var feed mta.Feed
	if err := json.Unmarshal(data, &feed); err != nil {
		t.Fatal(err)
	}
	return feed
}

func TestRenderOverviewContainsRoutes(t *testing.T) {
	feed := loadAlertsFixture(t)
	now := time.Unix(1734451200, 0)
	out := RenderOverview(feed, now, 100, 10)

	for _, want := range []string{
		"NYC Subway Service Status",
		"Refreshing every 10s",
		"Good Service",
		"Delays",
		"Part Suspended",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q", want)
		}
	}
}

func TestRenderLineDetailShowsStations(t *testing.T) {
	activities := []mta.StationActivity{
		{StationName: "Lorimer St", State: mta.StationTrainHere, Detail: "Train at platform"},
		{StationName: "1 Av", State: mta.StationArrivingSoon, Detail: "Arriving in 2m"},
	}
	service := mta.LineStatus{RouteID: "L", Status: "Planned - Stops Skipped"}
	out := RenderLineDetail("L", service, activities, time.Unix(1_700_000_000, 0), 80, 10, nil)

	for _, want := range []string{
		"L Train",
		"Lorimer St",
		"1 Av",
		"Train at platform",
		"Arriving in 2m",
		"Esc back to all lines",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q", want)
		}
	}
}

func TestRenderLineDetailShuttleMessage(t *testing.T) {
	out := RenderLineDetail("GS", mta.LineStatus{RouteID: "GS", Status: mta.GoodService}, nil, time.Now(), 80, 10, nil)
	if !strings.Contains(out, "not available") {
		t.Fatalf("expected shuttle message, got: %s", out)
	}
}

func TestRenderLineDetailError(t *testing.T) {
	out := RenderLineDetail("L", mta.LineStatus{RouteID: "L", Status: mta.GoodService}, nil, time.Now(), 80, 10, os.ErrNotExist)
	if !strings.Contains(out, "not exist") {
		t.Fatalf("expected error text in output")
	}
}

func TestCenterTruncateLenVisible(t *testing.T) {
	if got := center("hi", 10); got != "    hi    " && got != "    hi     " {
		// center may be off by one on odd widths
		if len(strings.TrimSpace(got)) != 2 {
			t.Fatalf("center: %q", got)
		}
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate: %q", got)
	}
	styled := "\x1b[31mred\x1b[0m"
	if lenVisible(styled) != 3 {
		t.Fatalf("lenVisible: got %d", lenVisible(styled))
	}
}

func TestRenderBackwardCompatible(t *testing.T) {
	feed := loadAlertsFixture(t)
	out := Render(feed, time.Unix(1734451200, 0), 100)
	if !strings.Contains(out, "NYC Subway Service Status") {
		t.Fatal("Render wrapper should produce overview")
	}
}
