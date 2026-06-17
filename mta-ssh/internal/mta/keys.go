package mta

import (
	"fmt"
	"strings"
)

// RouteFromKey maps a keyboard key to a subway route ID.
func RouteFromKey(key byte) (string, bool) {
	switch {
	case key >= '1' && key <= '7':
		return string(key), true
	case key >= 'a' && key <= 'z':
		r := strings.ToUpper(string(key))
		for _, route := range SubwayRoutes {
			if route == r {
				return route, true
			}
		}
	case key == 'i' || key == 'I':
		return "SI", true
	}
	return "", false
}

func SelectableRoutesHint() string {
	return "Type a line key (1-7, A-Z, I=SI) • Esc back"
}

func ShuttleDetailMessage(routeID string) string {
	switch routeID {
	case "GS", "H", "FS":
		return fmt.Sprintf("Live station tracking is not available for the %s shuttle.", RouteLabel(routeID))
	default:
		return ""
	}
}
