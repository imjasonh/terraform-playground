package mta

import "testing"

func TestRouteStyleForKnownRoutes(t *testing.T) {
	style := RouteStyleFor("1")
	if style.Background != "EE352E" || style.Foreground != "FFFFFF" {
		t.Fatalf("route 1 colors: %+v", style)
	}
	style = RouteStyleFor("N")
	if style.Background != "FCCC0A" || style.Foreground != "000000" {
		t.Fatalf("route N colors: %+v", style)
	}
}

func TestRouteStyleForUnknown(t *testing.T) {
	style := RouteStyleFor("ZZ")
	if style.Background != "808183" {
		t.Fatalf("unknown route fallback: %+v", style)
	}
}

func TestRouteLabel(t *testing.T) {
	tests := map[string]string{
		"1":  "1",
		"GS": "S",
		"FS": "S",
		"H":  "S",
		"SI": "SI",
		"L":  "L",
	}
	for route, want := range tests {
		if got := RouteLabel(route); got != want {
			t.Fatalf("RouteLabel(%q) = %q want %q", route, got, want)
		}
	}
}

func TestHexToRGB(t *testing.T) {
	r, g, b := HexToRGB("EE352E")
	if r != 238 || g != 53 || b != 46 {
		t.Fatalf("got (%d,%d,%d)", r, g, b)
	}
}
