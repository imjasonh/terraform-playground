package mta

import (
	"strconv"
	"strings"
	"time"
)

const GoodService = "Good Service"

type LineStatus struct {
	RouteID   string
	Status    string
	Severity  int
	Headline  string
	IsPlanned bool
}

func LineStatuses(feed Feed, now time.Time) []LineStatus {
	byRoute := make(map[string]LineStatus, len(SubwayRoutes))
	for _, routeID := range SubwayRoutes {
		byRoute[routeID] = LineStatus{
			RouteID: routeID,
			Status:  GoodService,
		}
	}

	for _, entry := range feed.Entity {
		if entry.Alert == nil {
			continue
		}
		alert := entry.Alert
		if !alertActive(alert, now) {
			continue
		}
		if !showInStatusBox(alert, now) {
			continue
		}

		alertType := strings.TrimSpace(alert.MercuryAlert.AlertType)
		if alertType == "" && alert.HeaderText != nil && len(alert.HeaderText.Translation) > 0 {
			alertType = strings.TrimSpace(alert.HeaderText.Translation[0].Text)
		}
		if alertType == "" {
			continue
		}

		headline := ""
		if alert.HeaderText != nil && len(alert.HeaderText.Translation) > 0 {
			headline = strings.TrimSpace(alert.HeaderText.Translation[0].Text)
		}

		isPlanned := strings.Contains(strings.ToLower(alertType), "planned")

		for _, entity := range alert.InformedEntity {
			routeID := strings.TrimSpace(entity.RouteID)
			if routeID == "" {
				continue
			}
			if entity.AgencyID != "" && entity.AgencyID != "MTASBWY" {
				continue
			}

			severity := parseSortOrder(entity.MercuryEntitySelector.SortOrder)
			current, ok := byRoute[routeID]
			if !ok {
				continue
			}
			if severity > current.Severity {
				byRoute[routeID] = LineStatus{
					RouteID:   routeID,
					Status:    alertType,
					Severity:  severity,
					Headline:  headline,
					IsPlanned: isPlanned,
				}
			}
		}
	}

	out := make([]LineStatus, 0, len(SubwayRoutes))
	for _, routeID := range SubwayRoutes {
		out = append(out, byRoute[routeID])
	}
	return out
}

func alertActive(alert *Alert, now time.Time) bool {
	if len(alert.ActivePeriod) == 0 {
		return true
	}
	nowUnix := now.Unix()
	for _, period := range alert.ActivePeriod {
		start := numberInt64(period.Start)
		end := numberInt64(period.End)
		if start > 0 && nowUnix < start {
			continue
		}
		if end > 0 && nowUnix > end {
			continue
		}
		return true
	}
	return false
}

func showInStatusBox(alert *Alert, now time.Time) bool {
	if alert.MercuryAlert.DisplayBeforeActive == nil {
		return false
	}
	displayBefore := *alert.MercuryAlert.DisplayBeforeActive
	if displayBefore == 0 {
		return true
	}
	if len(alert.ActivePeriod) == 0 {
		return true
	}
	nowUnix := now.Unix()
	for _, period := range alert.ActivePeriod {
		start := numberInt64(period.Start)
		if start == 0 {
			continue
		}
		if nowUnix >= start-int64(displayBefore) {
			return true
		}
	}
	return false
}

func parseSortOrder(sortOrder string) int {
	parts := strings.Split(sortOrder, ":")
	if len(parts) == 0 {
		return 0
	}
	last := parts[len(parts)-1]
	n, _ := strconv.Atoi(last)
	return n
}

func FeedTimestamp(feed Feed) time.Time {
	ts := numberInt64(feed.Header.Timestamp)
	if ts == 0 {
		return time.Now()
	}
	return time.Unix(ts, 0)
}
