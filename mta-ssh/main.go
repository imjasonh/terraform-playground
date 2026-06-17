package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/display"
	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
	"github.com/imjasonh/terraform-playground/mta-ssh/internal/sshserver"
)

func main() {
	addr := flag.String("addr", ":2222", "SSH listen address")
	feedURL := flag.String("feed-url", "", "MTA alerts feed URL (defaults to live API, or testdata when unavailable)")
	renderOnce := flag.Bool("render-once", false, "Print one frame to stdout and exit")
	width := flag.Int("width", 100, "Terminal width for render-once mode")
	flag.Parse()

	url := *feedURL
	if url == "" {
		url = os.Getenv("MTA_FEED_URL")
	}
	if url == "" {
		if _, err := os.Stat("testdata/sample-alerts.json"); err == nil {
			abs, _ := filepath.Abs("testdata/sample-alerts.json")
			url = "file://" + abs
		} else {
			url = mta.DefaultFeedURL
		}
	}

	client := mta.NewClient(url)

	if *renderOnce {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		feed, err := client.Fetch(ctx)
		if err != nil {
			log.Fatal(err)
		}
		now := mta.FeedTimestamp(feed)
		if now.IsZero() {
			now = time.Now()
		}
		_, _ = os.Stdout.WriteString(display.Render(feed, now, *width))
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := sshserver.New(*addr, client)
	if err != nil {
		log.Fatal(err)
	}
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}
