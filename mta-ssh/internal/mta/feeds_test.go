package mta

import (
	"strings"
	"testing"
)

func TestTripFeedURL(t *testing.T) {
	tests := map[string]string{
		"A":  "gtfs-ace",
		"C":  "gtfs-ace",
		"H":  "gtfs-ace",
		"B":  "gtfs-bdfm",
		"G":  "gtfs-g",
		"J":  "gtfs-jz",
		"L":  "gtfs-l",
		"N":  "gtfs-nqrw",
		"SI": "gtfs-si",
		"1":  "nyct%2Fgtfs",
		"4":  "nyct%2Fgtfs",
		"GS": "nyct%2Fgtfs",
	}

	for route, want := range tests {
		got := TripFeedURL(route)
		if got == "" {
			t.Fatalf("route %s: empty url", route)
		}
		if !strings.Contains(got, want) {
			t.Fatalf("route %s: got %q want substring %q", route, got, want)
		}
	}
}
