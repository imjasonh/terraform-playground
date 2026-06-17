package mta

import "testing"

func TestRouteFromKey(t *testing.T) {
	tests := []struct {
		key  byte
		route string
		ok   bool
	}{
		{'4', "4", true},
		{'l', "L", true},
		{'L', "L", true},
		{'i', "SI", true},
		{'I', "SI", true},
		{'x', "", false},
		{'0', "", false},
		{'8', "", false},
	}

	for _, tc := range tests {
		route, ok := RouteFromKey(tc.key)
		if ok != tc.ok || route != tc.route {
			t.Fatalf("RouteFromKey(%q) = (%q, %v) want (%q, %v)", tc.key, route, ok, tc.route, tc.ok)
		}
	}
}

func TestShuttleDetailMessage(t *testing.T) {
	if got := ShuttleDetailMessage("GS"); got == "" {
		t.Fatal("expected shuttle message for GS")
	}
	if got := ShuttleDetailMessage("4"); got != "" {
		t.Fatalf("expected empty for route 4, got %q", got)
	}
}

func TestSelectableRoutesHint(t *testing.T) {
	if SelectableRoutesHint() == "" {
		t.Fatal("expected non-empty hint")
	}
}
