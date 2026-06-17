package sshserver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
)

func fixtureURL(t *testing.T, parts ...string) string {
	t.Helper()
	candidates := []string{
		filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...),
		filepath.Join(append([]string{"testdata"}, parts...)...),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return "file://" + abs
		}
	}
	t.Fatalf("fixture not found: %v", parts)
	return ""
}

func TestSessionHandleKeySelectsAndClearsRoute(t *testing.T) {
	s := &Session{renderNow: make(chan struct{}, 1)}

	s.handleKey('4')
	if s.selectedRoute != "4" {
		t.Fatalf("selectedRoute = %q", s.selectedRoute)
	}

	s.handleKey('q')
	if s.selectedRoute != "" {
		t.Fatalf("expected cleared selection, got %q", s.selectedRoute)
	}

	s.handleKey(0x7f)
	s.handleKey('l')
	if s.selectedRoute != "L" {
		t.Fatalf("selectedRoute = %q", s.selectedRoute)
	}
}

func TestSessionHandleKeyIgnoresUnknown(t *testing.T) {
	s := &Session{renderNow: make(chan struct{}, 1)}
	s.handleKey('x')
	if s.selectedRoute != "" {
		t.Fatalf("unexpected selection %q", s.selectedRoute)
	}
}

func TestSessionPaintOverview(t *testing.T) {
	var buf bytes.Buffer
	s := &Session{
		AlertsClient: mta.NewClient(fixtureURL(t, "sample-alerts.json")),
		TripClient:   &mta.TripClient{FeedURL: fixtureURL(t, "sample-trips.pb")},
		RefreshEvery: 10 * time.Second,
		out:          &buf,
		width:        100,
	}
	s.paint()
	out := buf.String()
	if !strings.Contains(out, "NYC Subway Service Status") {
		t.Fatalf("overview not rendered: %q", out[:min(120, len(out))])
	}
}

func TestSessionPaintLineDetail(t *testing.T) {
	var buf bytes.Buffer
	s := &Session{
		AlertsClient: mta.NewClient(fixtureURL(t, "sample-alerts.json")),
		TripClient:   &mta.TripClient{FeedURL: fixtureURL(t, "sample-trips.pb")},
		RefreshEvery: 10 * time.Second,
		out:          &buf,
		width:        100,
		selectedRoute: "L",
	}
	s.paint()
	out := buf.String()
	if !strings.Contains(out, "Station Activity") {
		t.Fatalf("line detail not rendered")
	}
	if !strings.Contains(out, "Lorimer St") {
		t.Fatalf("missing station activity: %s", out)
	}
}

func TestSessionPaintShuttleSkipsTrips(t *testing.T) {
	var buf bytes.Buffer
	s := &Session{
		AlertsClient: mta.NewClient(fixtureURL(t, "sample-alerts.json")),
		TripClient:   &mta.TripClient{FeedURL: "file:///no/such/file.pb"},
		RefreshEvery: 10 * time.Second,
		out:          &buf,
		width:        100,
		selectedRoute: "GS",
	}
	s.paint()
	out := buf.String()
	if !strings.Contains(out, "not available") {
		t.Fatalf("expected shuttle message, got %q", out[:min(200, len(out))])
	}
}

func TestSessionPaintAlertsError(t *testing.T) {
	var buf bytes.Buffer
	s := &Session{
		AlertsClient: mta.NewClient("file:///no/such/alerts.json"),
		TripClient:   mta.NewTripClient(),
		RefreshEvery: time.Second,
		out:          &buf,
		width:        80,
	}
	s.paint()
	if !strings.Contains(buf.String(), "Error fetching alerts") {
		t.Fatal("expected alerts error message")
	}
}

func TestSessionTriggerRenderNonBlocking(t *testing.T) {
	s := &Session{renderNow: make(chan struct{}, 1)}
	s.triggerRender()
	s.triggerRender() // should not block
}

func TestNewServerDefaultsRefresh(t *testing.T) {
	srv, err := New(":0",
		mta.NewClient(fixtureURL(t, "sample-alerts.json")),
		mta.NewTripClient(),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if srv.RefreshEvery != 10*time.Second {
		t.Fatalf("refresh: %s", srv.RefreshEvery)
	}
}

func TestServerListenAndServeContextCancel(t *testing.T) {
	srv, err := New("127.0.0.1:0",
		mta.NewClient(fixtureURL(t, "sample-alerts.json")),
		mta.NewTripClient(),
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ListenAndServe: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server shutdown")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
