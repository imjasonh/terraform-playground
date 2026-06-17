package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
)

const overviewTmpl = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  body { margin: 0; background: #0d1117; display: flex; justify-content: center; align-items: center; min-height: 100vh; font-family: "JetBrains Mono", "SF Mono", "Menlo", "Consolas", monospace; }
  .terminal { width: 980px; background: #0d1117; border: 1px solid #30363d; border-radius: 10px; box-shadow: 0 20px 60px rgba(0,0,0,0.45); overflow: hidden; }
  .titlebar { background: #161b22; padding: 10px 14px; display: flex; gap: 8px; border-bottom: 1px solid #30363d; }
  .dot { width: 12px; height: 12px; border-radius: 50%; }
  .red { background: #ff5f56; } .yellow { background: #ffbd2e; } .green { background: #27c93f; }
  .content { padding: 24px 28px 28px; color: #e6edf3; }
  h1 { margin: 0 0 6px; font-size: 22px; text-align: center; font-weight: 700; }
  .sub { margin: 0 0 18px; text-align: center; color: #8b949e; font-size: 13px; }
  hr { border: none; border-top: 1px solid #30363d; margin: 0 0 16px; }
  .grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px 14px; }
  .row { display: flex; align-items: center; gap: 10px; min-height: 28px; font-size: 14px; }
  .badge { display: inline-flex; align-items: center; justify-content: center; min-width: 30px; height: 22px; border-radius: 3px; font-weight: 700; font-size: 13px; padding: 0 6px; }
  .status { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .good { color: #3fb950; } .delay { color: #d29922; font-weight: 700; } .suspend { color: #f85149; font-weight: 700; } .planned { color: #79c0ff; font-weight: 700; }
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
    <p class="footer">Refreshing every 10s • Type a line key (1-7, A-Z, I=SI) • Esc back</p>
  </div>
</div>
</body>
</html>`

const lineDetailTmpl = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  body { margin: 0; background: #0d1117; display: flex; justify-content: center; align-items: center; min-height: 100vh; font-family: "JetBrains Mono", "SF Mono", "Menlo", "Consolas", monospace; }
  .terminal { width: 720px; background: #0d1117; border: 1px solid #30363d; border-radius: 10px; box-shadow: 0 20px 60px rgba(0,0,0,0.45); overflow: hidden; }
  .titlebar { background: #161b22; padding: 10px 14px; display: flex; gap: 8px; border-bottom: 1px solid #30363d; }
  .dot { width: 12px; height: 12px; border-radius: 50%; }
  .red { background: #ff5f56; } .yellow { background: #ffbd2e; } .green { background: #27c93f; }
  .content { padding: 24px 28px 28px; color: #e6edf3; }
  h1 { margin: 0 0 10px; font-size: 22px; text-align: center; font-weight: 700; }
  .service { display: flex; align-items: center; justify-content: center; gap: 10px; margin-bottom: 8px; font-size: 15px; }
  .badge { display: inline-flex; align-items: center; justify-content: center; min-width: 30px; height: 22px; border-radius: 3px; font-weight: 700; font-size: 13px; padding: 0 6px; }
  .sub { margin: 0 0 18px; text-align: center; color: #8b949e; font-size: 13px; }
  hr { border: none; border-top: 1px solid #30363d; margin: 0 0 16px; }
  .stations { display: flex; flex-direction: column; gap: 12px; padding: 4px 0; }
  .station { display: flex; align-items: baseline; gap: 12px; font-size: 15px; }
  .icon { width: 18px; text-align: center; font-size: 16px; flex-shrink: 0; }
  .here { color: #50fa7b; font-weight: 700; }
  .soon { color: #f1c04e; font-weight: 600; }
  .name { color: #e6edf3; font-weight: 600; min-width: 200px; }
  .detail { color: #8b949e; font-size: 13px; }
  .footer { margin-top: 20px; text-align: center; color: #6e7681; font-size: 12px; }
</style>
</head>
<body>
<div class="terminal">
  <div class="titlebar"><div class="dot red"></div><div class="dot yellow"></div><div class="dot green"></div></div>
  <div class="content">
    <h1>{{.Label}} Train — Station Activity</h1>
    <div class="service">
      <span class="badge" style="background:#{{.BG}};color:#{{.FG}}">{{.Label}}</span>
      <span class="{{.ServiceClass}}">{{.ServiceStatus}}</span>
    </div>
    <p class="sub">Updated {{.Updated}}</p>
    <hr>
    <div class="stations">
      {{range .Stations}}
      <div class="station">
        <span class="icon {{.IconClass}}">{{.Icon}}</span>
        <span class="name">{{.Name}}</span>
        <span class="detail">{{.Detail}}</span>
      </div>
      {{end}}
    </div>
    <p class="footer">Refreshing every 10s • Esc back to all lines</p>
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

type stationView struct {
	Icon      string
	IconClass string
	Name      string
	Detail    string
}

type overviewData struct {
	Updated string
	Lines   []lineView
}

type lineDetailData struct {
	Label         string
	BG            string
	FG            string
	ServiceStatus string
	ServiceClass  string
	Updated       string
	Stations      []stationView
}

func main() {
	line := flag.String("line", "", "Render drilled-in view for this route (e.g. L, 4, N)")
	flag.Parse()

	now := time.Unix(1_700_000_000, 0)
	outDir := "/opt/cursor/artifacts/screenshots"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}

	if *line != "" {
		path, err := renderLineDetail(strings.ToUpper(*line), now, outDir)
		if err != nil {
			panic(err)
		}
		fmt.Println(path)
		return
	}

	path, err := renderOverview(now, outDir)
	if err != nil {
		panic(err)
	}
	fmt.Println(path)
}

func renderOverview(now time.Time, outDir string) (string, error) {
	alertsURL := fixtureURL("testdata/sample-alerts.json")
	client := mta.NewClient(alertsURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	feed, err := client.Fetch(ctx)
	if err != nil {
		return "", err
	}
	if ts := mta.FeedTimestamp(feed); !ts.IsZero() {
		now = ts
	}

	views := make([]lineView, 0)
	for _, line := range mta.LineStatuses(feed, now) {
		style := mta.RouteStyleFor(line.RouteID)
		views = append(views, lineView{
			Label:  mta.RouteLabel(line.RouteID),
			BG:     style.Background,
			FG:     style.Foreground,
			Status: line.Status,
			Class:  statusClass(line.Status),
		})
	}

	data := overviewData{
		Updated: now.In(nyLocation()).Format("Mon Jan 2, 3:04:05 PM MST"),
		Lines:   views,
	}

	outPath := filepath.Join(outDir, "mta-ssh-preview.html")
	return outPath, writeTemplate(overviewTmpl, outPath, data)
}

func renderLineDetail(routeID string, now time.Time, outDir string) (string, error) {
	alertsURL := fixtureURL("testdata/sample-alerts.json")
	tripURL := fixtureURL("testdata/sample-trips.pb")

	alertsClient := mta.NewClient(alertsURL)
	tripClient := mta.NewTripClient()
	tripClient.FeedURL = tripURL

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	feed, err := alertsClient.Fetch(ctx)
	if err != nil {
		return "", err
	}

	service := mta.LineStatus{RouteID: routeID, Status: mta.GoodService}
	for _, line := range mta.LineStatuses(feed, now) {
		if line.RouteID == routeID {
			service = line
			break
		}
	}

	tripFeed, err := tripClient.FetchRoute(ctx, routeID)
	if err != nil {
		return "", err
	}
	activities, err := mta.StationActivityForRoute(tripFeed, routeID, now)
	if err != nil {
		return "", err
	}

	style := mta.RouteStyleFor(routeID)
	stations := make([]stationView, 0, len(activities))
	for _, act := range activities {
		icon, iconClass := stationIcon(act.State)
		stations = append(stations, stationView{
			Icon:      icon,
			IconClass: iconClass,
			Name:      act.StationName,
			Detail:    act.Detail,
		})
	}

	data := lineDetailData{
		Label:         mta.RouteLabel(routeID),
		BG:            style.Background,
		FG:            style.Foreground,
		ServiceStatus: service.Status,
		ServiceClass:  statusClass(service.Status),
		Updated:       now.In(nyLocation()).Format("3:04:05 PM MST"),
		Stations:      stations,
	}

	outPath := filepath.Join(outDir, "mta-ssh-line-preview.html")
	return outPath, writeTemplate(lineDetailTmpl, outPath, data)
}

func writeTemplate(tmplText, outPath string, data any) error {
	tmpl := template.Must(template.New("page").Parse(tmplText))
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, data)
}

func fixtureURL(rel string) string {
	for _, c := range []string{rel, filepath.Join("mta-ssh", rel), filepath.Join("/workspace/mta-ssh", rel)} {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return "file://" + abs
		}
	}
	return ""
}

func stationIcon(state string) (string, string) {
	switch state {
	case mta.StationTrainHere:
		return "●", "here"
	case mta.StationArrivingSoon:
		return "↓", "soon"
	default:
		return "·", ""
	}
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
