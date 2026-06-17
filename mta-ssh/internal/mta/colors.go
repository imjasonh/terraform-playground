package mta

import "fmt"

// RouteStyle holds MTA official route colors from routes.txt / colors.csv.
type RouteStyle struct {
	Background string // hex without #
	Foreground string // hex without #
}

var routeStyles = map[string]RouteStyle{
	"1":  {Background: "EE352E", Foreground: "FFFFFF"},
	"2":  {Background: "EE352E", Foreground: "FFFFFF"},
	"3":  {Background: "EE352E", Foreground: "FFFFFF"},
	"4":  {Background: "00933C", Foreground: "FFFFFF"},
	"5":  {Background: "00933C", Foreground: "FFFFFF"},
	"6":  {Background: "00933C", Foreground: "FFFFFF"},
	"7":  {Background: "B933AD", Foreground: "FFFFFF"},
	"A":  {Background: "0039A6", Foreground: "FFFFFF"},
	"C":  {Background: "0039A6", Foreground: "FFFFFF"},
	"E":  {Background: "0039A6", Foreground: "FFFFFF"},
	"B":  {Background: "FF6319", Foreground: "000000"},
	"D":  {Background: "FF6319", Foreground: "000000"},
	"F":  {Background: "FF6319", Foreground: "000000"},
	"M":  {Background: "FF6319", Foreground: "000000"},
	"G":  {Background: "6CBE45", Foreground: "000000"},
	"J":  {Background: "996633", Foreground: "FFFFFF"},
	"Z":  {Background: "996633", Foreground: "FFFFFF"},
	"L":  {Background: "A7A9AC", Foreground: "000000"},
	"N":  {Background: "FCCC0A", Foreground: "000000"},
	"Q":  {Background: "FCCC0A", Foreground: "000000"},
	"R":  {Background: "FCCC0A", Foreground: "000000"},
	"W":  {Background: "FCCC0A", Foreground: "000000"},
	"GS": {Background: "808183", Foreground: "FFFFFF"},
	"H":  {Background: "808183", Foreground: "FFFFFF"},
	"FS": {Background: "808183", Foreground: "FFFFFF"},
	"SI": {Background: "808183", Foreground: "FFFFFF"},
}

// SubwayRoutes is the display order used by the MTA service status box.
var SubwayRoutes = []string{
	"1", "2", "3",
	"4", "5", "6",
	"7",
	"A", "C", "E",
	"B", "D", "F", "M",
	"G",
	"J", "Z",
	"L",
	"N", "Q", "R", "W",
	"GS", "H", "FS",
	"SI",
}

func RouteStyleFor(routeID string) RouteStyle {
	if style, ok := routeStyles[routeID]; ok {
		return style
	}
	return RouteStyle{Background: "808183", Foreground: "FFFFFF"}
}

func RouteLabel(routeID string) string {
	switch routeID {
	case "GS":
		return "S"
	case "FS":
		return "S"
	case "H":
		return "S"
	case "SI":
		return "SI"
	default:
		return routeID
	}
}

func HexToRGB(hex string) (r, g, b int) {
	if len(hex) == 6 {
		fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	}
	return r, g, b
}
