package mta

import (
	"testing"
	"time"
)

func TestParentStopID(t *testing.T) {
	tests := map[string]string{
		"L10S": "L10",
		"L10N": "L10",
		"L10":  "L10",
		"101":  "101",
	}
	for in, want := range tests {
		if got := parentStopID(in); got != want {
			t.Fatalf("parentStopID(%q) = %q want %q", in, got, want)
		}
	}
}

func TestStopSortKey(t *testing.T) {
	if stopSortKey("L02") >= stopSortKey("L10") {
		t.Fatal("L02 should sort before L10")
	}
	if stopSortKey("401") >= stopSortKey("410") {
		t.Fatal("401 should sort before 410")
	}
}

func TestStopMatchesRoute(t *testing.T) {
	tests := []struct {
		stop, route string
		want        bool
	}{
		{"L08", "L", true},
		{"L08", "4", false},
		{"401", "4", true},
		{"401", "1", false},
		{"A02", "A", true},
		{"S01", "SI", false},
		{"S09", "SI", true},
		{"S01", "GS", false},
	}

	for _, tc := range tests {
		if got := stopMatchesRoute(tc.stop, tc.route); got != tc.want {
			t.Fatalf("stopMatchesRoute(%q, %q) = %v want %v", tc.stop, tc.route, got, tc.want)
		}
	}
}

func TestLoadStationIndexFromFixture(t *testing.T) {
	idx, err := loadStationIndexFrom(testdataPath(t, "gtfs", "stops.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if got := idx.Name("L08S"); got != "Bedford Av" {
		t.Fatalf("Name(L08S): got %q", got)
	}

	stations := idx.RouteStations("L")
	if len(stations) < 10 {
		t.Fatalf("expected many L stations, got %d", len(stations))
	}
	if stations[0].Name == "" {
		t.Fatal("expected named stations")
	}
	if idx.ParentOrder("L08") >= idx.ParentOrder("L10") {
		t.Fatal("L08 should appear before L10 along the line")
	}
}

func TestStationsSingleton(t *testing.T) {
	a, err := Stations()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Stations()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("expected cached singleton")
	}
}

func TestStationIndexNameFallback(t *testing.T) {
	idx := &StationIndex{names: map[string]string{"L08": "Bedford Av"}}
	if got := idx.Name("L08S"); got != "Bedford Av" {
		t.Fatalf("got %q", got)
	}
	if got := idx.Name("UNKNOWN"); got != "UNKNOWN" {
		t.Fatalf("got %q", got)
	}
}

func TestStationIndexParentOrderMissing(t *testing.T) {
	idx := &StationIndex{parentOrder: map[string]int{}}
	if got := idx.ParentOrder("L99"); got != 9999 {
		t.Fatalf("got %d", got)
	}
}

// Ensure station activity can resolve names from fixture GTFS.
func TestStationIndexIntegrationWithTrips(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	activities, err := StationActivityForRoute(sampleTripFeed(now), "L", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, act := range activities {
		if act.StationName == act.StopID {
			t.Fatalf("expected resolved name, got stop id %q", act.StopID)
		}
	}
}
