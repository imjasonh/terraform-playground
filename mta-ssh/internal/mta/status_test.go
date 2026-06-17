package mta

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestLineStatusesFromFixture(t *testing.T) {
	data, err := os.ReadFile(testdataPath(t, "sample-alerts.json"))
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

func TestLineStatusesPicksHigherSeverity(t *testing.T) {
	zero := 0
	feed := Feed{
		Entity: []Entry{
			{
				ID: "a",
				Alert: &Alert{
					ActivePeriod: []ActivePeriod{{Start: "1"}},
					InformedEntity: []InformedEntity{{
						RouteID: "2",
						MercuryEntitySelector: MercuryEntitySelector{SortOrder: "MTASBWY:2:16"},
					}},
					MercuryAlert: MercuryAlert{AlertType: "Slow Speeds", DisplayBeforeActive: &zero},
				},
			},
			{
				ID: "b",
				Alert: &Alert{
					ActivePeriod: []ActivePeriod{{Start: "1"}},
					InformedEntity: []InformedEntity{{
						RouteID: "2",
						MercuryEntitySelector: MercuryEntitySelector{SortOrder: "MTASBWY:2:34"},
					}},
					MercuryAlert: MercuryAlert{AlertType: "Part Suspended", DisplayBeforeActive: &zero},
				},
			},
		},
	}

	now := time.Unix(100, 0)
	for _, line := range LineStatuses(feed, now) {
		if line.RouteID == "2" && line.Status != "Part Suspended" {
			t.Fatalf("route 2: got %q", line.Status)
		}
	}
}

func TestLineStatusesIgnoresNonSubwayAgency(t *testing.T) {
	zero := 0
	feed := Feed{
		Entity: []Entry{{
			ID: "a",
			Alert: &Alert{
				ActivePeriod: []ActivePeriod{{Start: "1"}},
				InformedEntity: []InformedEntity{{
					AgencyID: "LI",
					RouteID:  "4",
					MercuryEntitySelector: MercuryEntitySelector{SortOrder: "LI:4:39"},
				}},
				MercuryAlert: MercuryAlert{AlertType: "Suspended", DisplayBeforeActive: &zero},
			},
		}},
	}
	for _, line := range LineStatuses(feed, time.Unix(100, 0)) {
		if line.RouteID == "4" && line.Status != GoodService {
			t.Fatalf("route 4 should ignore LIRR alert, got %q", line.Status)
		}
	}
}

func TestAlertActive(t *testing.T) {
	now := time.Unix(1000, 0)
	if !alertActive(&Alert{ActivePeriod: []ActivePeriod{}}, now) {
		t.Fatal("empty active period should be active")
	}
	if !alertActive(&Alert{ActivePeriod: []ActivePeriod{{Start: "500"}}}, now) {
		t.Fatal("started alert should be active")
	}
	if alertActive(&Alert{ActivePeriod: []ActivePeriod{{Start: "2000"}}}, now) {
		t.Fatal("future alert should be inactive")
	}
	if alertActive(&Alert{ActivePeriod: []ActivePeriod{{Start: "1", End: "500"}}}, now) {
		t.Fatal("ended alert should be inactive")
	}
}

func TestShowInStatusBox(t *testing.T) {
	zero := 0
	oneHour := 3600
	now := time.Unix(5000, 0)

	if showInStatusBox(&Alert{MercuryAlert: MercuryAlert{}}, now) {
		t.Fatal("nil displayBeforeActive should not show")
	}
	if !showInStatusBox(&Alert{MercuryAlert: MercuryAlert{DisplayBeforeActive: &zero}}, now) {
		t.Fatal("zero displayBeforeActive should show")
	}
	if !showInStatusBox(&Alert{
		ActivePeriod:   []ActivePeriod{{Start: "6000"}},
		MercuryAlert:   MercuryAlert{DisplayBeforeActive: &oneHour},
	}, now) {
		t.Fatal("planned alert within display window should show")
	}
	if showInStatusBox(&Alert{
		ActivePeriod: []ActivePeriod{{Start: "20000"}},
		MercuryAlert: MercuryAlert{DisplayBeforeActive: &oneHour},
	}, now) {
		t.Fatal("planned alert outside display window should not show")
	}
}

func TestParseSortOrder(t *testing.T) {
	tests := map[string]int{
		"MTASBWY:D:26": 26,
		"":             0,
		"bad":          0,
		"MTASBWY:G:16": 16,
	}
	for in, want := range tests {
		if got := parseSortOrder(in); got != want {
			t.Fatalf("parseSortOrder(%q) = %d want %d", in, got, want)
		}
	}
}

func TestFeedTimestamp(t *testing.T) {
	feed := Feed{Header: Header{Timestamp: "1700000000"}}
	got := FeedTimestamp(feed)
	if got.Unix() != 1700000000 {
		t.Fatalf("got %d", got.Unix())
	}
}

func TestFeedTimestampZeroUsesNow(t *testing.T) {
	before := time.Now()
	got := FeedTimestamp(Feed{})
	if got.Before(before) {
		t.Fatal("zero timestamp should default to now")
	}
}
