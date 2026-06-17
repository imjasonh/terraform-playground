package main

import (
	"context"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
)

const pageTmpl = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: #0d1117;
    display: flex;
    justify-content: center;
    align-items: center;
    min-height: 100vh;
    font-family: "JetBrains Mono", "SF Mono", "Menlo", "Consolas", monospace;
  }
  .terminal {
    width: 980px;
    background: #0d1117;
    border: 1px solid #30363d;
    border-radius: 10px;
    box-shadow: 0 20px 60px rgba(0,0,0,0.45);
    overflow: hidden;
  }
  .titlebar {
    background: #161b22;
    padding: 10px 14px;
    display: flex;
    gap: 8px;
    border-bottom: 1px solid #30363d;
  }
  .dot { width: 12px; height: 12px; border-radius: 50%; }
  .red { background: #ff5f56; }
  .yellow { background: #ffbd2e; }
  .green { background: #27c93f; }
  .content { padding: 24px 28px 28px; color: #e6edf3; }
  h1 { margin: 0 0 6px; font-size: 22px; text-align: center; font-weight: 700; }
  .sub { margin: 0 0 18px; text-align: center; color: #8b949e; font-size: 13px; }
  hr { border: none; border-top: 1px solid #30363d; margin: 0 0 16px; }
  .grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px 14px; }
  .row { display: flex; align-items: center; gap: 10px; min-height: 28px; font-size: 14px; }
  .badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 30px;
    height: 22px;
    border-radius: 3px;
    font-weight: 700;
    font-size: 13px;
    padding: 0 6px;
  }
  .status { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .good { color: #3fb950; }
  .delay { color: #d29922; font-weight: 700; }
  .suspend { color: #f85149; font-weight: 700; }
  .planned { color: #79c0ff; font-weight: 700; }
  .footer { margin-top: 18px; text-align: center; color: #6e7681; font-size: 12px; }
</style>
</head>
<body>
<div class="terminal">
  <div class="titlebar"><div class="dot red"></div><div class="dot yellow"></div><div class="dot green"></div></div>
  <div class="content">
    <h1>NYC Subway Service Status</h1>
    <p class="sub">Live from MTA GTFS-RT • Updated {{.Updated}}</p>
    <hr>
    <div class="grid">
      {{range .Lines}}
      <div class="row">
        <span class="badge" style="background:#{{.BG}};color:#{{.FG}}">{{.Label}}</span>
        <span class="status {{.Class}}">{{.Status}}</span>
      </div>
      {{end}}
    </div>
    <p class="footer">Refreshing every 30s • ssh mta-ssh for live updates</p>
  </div>
</div>
</body>
</html>`

type lineView struct {
	Label  string
	BG     string
	FG     string
	Status string
	Class  string
}

type pageData struct {
	Updated string
	Lines   []lineView
}

func main() {
	url := os.Getenv("MTA_FEED_URL")
	if url == "" {
		abs, _ := filepath.Abs("testdata/sample-alerts.json")
		url = "file://" + abs
	}

	client := mta.NewClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	feed, err := client.Fetch(ctx)
	if err != nil {
		panic(err)
	}

	now := mta.FeedTimestamp(feed)
	lines := mta.LineStatuses(feed, now)

	views := make([]lineView, 0, len(lines))
	for _, line := range lines {
		style := mta.RouteStyleFor(line.RouteID)
		views = append(views, lineView{
			Label:  mta.RouteLabel(line.RouteID),
			BG:     style.Background,
			FG:     style.Foreground,
			Status: line.Status,
			Class:  statusClass(line.Status),
		})
	}

	data := pageData{
		Updated: now.In(nyLocation()).Format("Mon Jan 2, 3:04:05 PM MST"),
		Lines:   views,
	}

	tmpl := template.Must(template.New("page").Parse(pageTmpl))
	outPath := "/opt/cursor/artifacts/screenshots/mta-ssh-preview.html"
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		panic(err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := tmpl.Execute(f, data); err != nil {
		panic(err)
	}
	fmt.Println(outPath)
}

func statusClass(status string) string {
	lower := strings.ToLower(status)
	switch {
	case status == mta.GoodService:
		return "good"
	case strings.Contains(lower, "suspend"):
		return "suspend"
	case strings.Contains(lower, "delay") || strings.Contains(lower, "slow"):
		return "delay"
	case strings.Contains(lower, "planned"):
		return "planned"
	default:
		return ""
	}
}

func nyLocation() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("EST", -5*3600)
	}
	return loc
}
