package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/display"
	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
	"github.com/imjasonh/terraform-playground/mta-ssh/internal/sshserver"
)

func main() {
	addr := flag.String("addr", ":2222", "SSH listen address")
	feedURL := flag.String("feed-url", "", "MTA alerts feed URL")
	tripFeedURL := flag.String("trip-feed-url", "", "MTA trip updates feed URL (protobuf, file:// for fixtures)")
	refreshSec := flag.Int("refresh", defaultRefreshSec(), "Screen refresh interval in seconds")
	renderOnce := flag.Bool("render-once", false, "Print one frame to stdout and exit")
	width := flag.Int("width", 100, "Terminal width for render-once mode")
	flag.Parse()

	alertsURL := *feedURL
	if alertsURL == "" {
		alertsURL = os.Getenv("MTA_FEED_URL")
	}
	if alertsURL == "" {
		alertsURL = defaultAlertsFixture()
	}

	alertsClient := mta.NewClient(alertsURL)
	tripClient := mta.NewTripClient()
	if *tripFeedURL != "" {
		tripClient.FeedURL = *tripFeedURL
	} else if u := os.Getenv("MTA_TRIP_FEED_URL"); u != "" {
		tripClient.FeedURL = u
	} else if fixture := defaultTripFixture(); fixture != "" {
		tripClient.FeedURL = "file://" + fixture
	}

	if *renderOnce {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		feed, err := alertsClient.Fetch(ctx)
		if err != nil {
			log.Fatal(err)
		}
		now := mta.FeedTimestamp(feed)
		if now.IsZero() {
			now = time.Now()
		}
		_, _ = os.Stdout.WriteString(display.RenderOverview(feed, now, *width, *refreshSec))
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := sshserver.New(*addr, alertsClient, tripClient, time.Duration(*refreshSec)*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}

func defaultRefreshSec() int {
	if v := os.Getenv("MTA_REFRESH_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 10
}

func defaultAlertsFixture() string {
	if path := findFixture("testdata/sample-alerts.json"); path != "" {
		return "file://" + path
	}
	return mta.DefaultFeedURL
}

func defaultTripFixture() string {
	return findFixture("testdata/sample-trips.pb")
}

func findFixture(rel string) string {
	candidates := []string{rel, filepath.Join("mta-ssh", rel), filepath.Join("/workspace/mta-ssh", rel)}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}
