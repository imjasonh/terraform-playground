package display

import (
	"fmt"
	"strings"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
)

const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	dim     = "\x1b[2m"
	clear   = "\x1b[2J\x1b[H"
	hideCur = "\x1b[?25l"
	showCur = "\x1b[?25h"
)

func RenderOverview(feed mta.Feed, now time.Time, width int, refreshSec int) string {
	if width < 60 {
		width = 80
	}

	lines := mta.LineStatuses(feed, now)
	updated := mta.FeedTimestamp(feed).In(nyLocation())

	var b strings.Builder
	b.WriteString(clear)
	b.WriteString(hideCur)

	title := "NYC Subway Service Status"
	b.WriteString(bold)
	b.WriteString(center(title, width))
	b.WriteString(reset)
	b.WriteString("\n")

	subtitle := fmt.Sprintf("Live from MTA GTFS-RT  •  Updated %s", updated.Format("Mon Jan 2, 3:04:05 PM MST"))
	b.WriteString(dim)
	b.WriteString(center(subtitle, width))
	b.WriteString(reset)
	b.WriteString("\n\n")

	b.WriteString(dim)
	b.WriteString(strings.Repeat("─", width))
	b.WriteString(reset)
	b.WriteString("\n")

	cols := 3
	if width >= 100 {
		cols = 4
	}
	colWidth := (width - 2) / cols

	for i := 0; i < len(lines); i += cols {
		for c := 0; c < cols; c++ {
			idx := i + c
			if idx >= len(lines) {
				break
			}
			cell := renderLineCell(lines[idx], colWidth-2)
			b.WriteString(" ")
			b.WriteString(cell)
			if c < cols-1 {
				b.WriteString(strings.Repeat(" ", colWidth-lenVisible(cell)))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dim)
	footer := fmt.Sprintf("Refreshing every %ds  •  %s", refreshSec, mta.SelectableRoutesHint())
	b.WriteString(center(footer, width))
	b.WriteString(reset)
	b.WriteString("\n")
	b.WriteString(showCur)

	return b.String()
}

func RenderLineDetail(routeID string, service mta.LineStatus, activities []mta.StationActivity, now time.Time, width int, refreshSec int, err error) string {
	if width < 60 {
		width = 80
	}

	style := mta.RouteStyleFor(routeID)
	label := mta.RouteLabel(routeID)
	bgR, bgG, bgB := mta.HexToRGB(style.Background)
	fgR, fgG, fgB := mta.HexToRGB(style.Foreground)
	badge := fmt.Sprintf("\x1b[48;2;%d;%d;%dm\x1b[38;2;%d;%d;%dm %s \x1b[0m", bgR, bgG, bgB, fgR, fgG, fgB, label)

	var b strings.Builder
	b.WriteString(clear)
	b.WriteString(hideCur)

	title := fmt.Sprintf("%s Train — Station Activity", label)
	b.WriteString(bold)
	b.WriteString(center(title, width))
	b.WriteString(reset)
	b.WriteString("\n")

	statusLine := badge + " " + service.Status
	b.WriteString(center(statusLine, width))
	b.WriteString("\n")

	subtitle := fmt.Sprintf("Updated %s", now.In(nyLocation()).Format("3:04:05 PM MST"))
	b.WriteString(dim)
	b.WriteString(center(subtitle, width))
	b.WriteString(reset)
	b.WriteString("\n\n")

	b.WriteString(dim)
	b.WriteString(strings.Repeat("─", width))
	b.WriteString(reset)
	b.WriteString("\n")

	if msg := mta.ShuttleDetailMessage(routeID); msg != "" {
		b.WriteString("\n  ")
		b.WriteString(dim)
		b.WriteString(msg)
		b.WriteString(reset)
		b.WriteString("\n")
	} else if err != nil {
		b.WriteString("\n  \x1b[31m")
		b.WriteString(truncate(err.Error(), width-4))
		b.WriteString(reset)
		b.WriteString("\n")
	} else if len(activities) == 0 {
		b.WriteString("\n  ")
		b.WriteString(dim)
		b.WriteString("No trains at stations or arriving in the next 5 minutes.")
		b.WriteString(reset)
		b.WriteString("\n")
	} else {
		b.WriteString("\n")
		for _, act := range activities {
			icon, tone := stationIcon(act.State)
			line := fmt.Sprintf("  %s  %s", icon, act.StationName)
			b.WriteString(tone)
			b.WriteString(truncate(line, width-8))
			b.WriteString(reset)
			b.WriteString(dim)
			b.WriteString("  ")
			b.WriteString(act.Detail)
			b.WriteString(reset)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dim)
	footer := fmt.Sprintf("Refreshing every %ds  •  Esc back to all lines", refreshSec)
	b.WriteString(center(footer, width))
	b.WriteString(reset)
	b.WriteString("\n")
	b.WriteString(showCur)

	return b.String()
}

func stationIcon(state string) (string, string) {
	switch state {
	case mta.StationTrainHere:
		return "●", "\x1b[38;2;80;220;120m\x1b[1m"
	case mta.StationArrivingSoon:
		return "↓", "\x1b[38;2;255;200;80m"
	default:
		return "·", dim
	}
}

// Render keeps the render-once overview API.
func Render(feed mta.Feed, now time.Time, width int) string {
	return RenderOverview(feed, now, width, 10)
}

func renderLineCell(line mta.LineStatus, width int) string {
	style := mta.RouteStyleFor(line.RouteID)
	label := mta.RouteLabel(line.RouteID)
	bgR, bgG, bgB := mta.HexToRGB(style.Background)
	fgR, fgG, fgB := mta.HexToRGB(style.Foreground)

	badge := fmt.Sprintf("\x1b[48;2;%d;%d;%dm\x1b[38;2;%d;%d;%dm %s \x1b[0m",
		bgR, bgG, bgB, fgR, fgG, fgB, label)

	statusColor := statusTone(line)
	status := truncate(line.Status, width-6)
	if line.Status == mta.GoodService {
		status = statusColor + status + reset
	} else {
		status = statusColor + bold + status + reset + dim
	}

	return badge + " " + status
}

func statusTone(line mta.LineStatus) string {
	lower := strings.ToLower(line.Status)
	switch {
	case line.Status == mta.GoodService:
		return "\x1b[38;2;80;200;120m"
	case strings.Contains(lower, "suspend"):
		return "\x1b[38;2;255;80;80m"
	case strings.Contains(lower, "delay") || strings.Contains(lower, "slow"):
		return "\x1b[38;2;255;180;60m"
	case strings.Contains(lower, "planned"):
		return "\x1b[38;2;120;170;255m"
	case strings.Contains(lower, "notice") || strings.Contains(lower, "boarding"):
		return "\x1b[38;2;180;180;180m"
	default:
		return "\x1b[38;2;220;220;220m"
	}
}

func center(text string, width int) string {
	visible := len(text)
	if visible >= width {
		return text
	}
	pad := (width - visible) / 2
	return strings.Repeat(" ", pad) + text
}

func truncate(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func lenVisible(s string) int {
	inEsc := false
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if s[i] == 'm' {
				inEsc = false
			}
			continue
		}
		count++
	}
	return count
}

func nyLocation() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("EST", -5*3600)
	}
	return loc
}
