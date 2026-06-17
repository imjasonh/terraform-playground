package mta

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Station struct {
	ID   string
	Name string
}

type StationIndex struct {
	names       map[string]string
	routeStops  map[string][]Station
	parentOrder map[string]int
}

var (
	stationOnce  sync.Once
	stationIndex *StationIndex
	stationErr   error
)

func Stations() (*StationIndex, error) {
	stationOnce.Do(func() {
		path := os.Getenv("MTA_GTFS_STOPS")
		if path == "" {
			candidates := []string{
				"testdata/gtfs/stops.txt",
				filepath.Join("mta-ssh", "testdata/gtfs/stops.txt"),
				"/workspace/mta-ssh/testdata/gtfs/stops.txt",
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					path = c
					break
				}
			}
		}
		if path == "" {
			stationErr = fmt.Errorf("stops.txt not found; set MTA_GTFS_STOPS")
			return
		}
		stationIndex, stationErr = loadStationIndexFrom(path)
	})
	return stationIndex, stationErr
}

func loadStationIndexFrom(path string) (*StationIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("stops.txt is empty")
	}

	idx := &StationIndex{
		names:       make(map[string]string),
		routeStops:  make(map[string][]Station),
		parentOrder: make(map[string]int),
	}

	for _, row := range records[1:] {
		if len(row) < 9 {
			continue
		}
		stopID := row[0]
		name := row[2]
		locationType := row[8]
		idx.names[stopID] = name
		if locationType != "1" {
			continue
		}
		for _, routeID := range SubwayRoutes {
			if stopMatchesRoute(stopID, routeID) {
				idx.routeStops[routeID] = append(idx.routeStops[routeID], Station{ID: stopID, Name: name})
			}
		}
	}

	for routeID, stops := range idx.routeStops {
		sort.Slice(stops, func(i, j int) bool {
			return stopSortKey(stops[i].ID) < stopSortKey(stops[j].ID)
		})
		idx.routeStops[routeID] = stops
		for i, st := range stops {
			idx.parentOrder[st.ID] = i
		}
	}

	return idx, nil
}

func stopMatchesRoute(stopID, routeID string) bool {
	switch routeID {
	case "SI":
		if !strings.HasPrefix(stopID, "S") {
			return false
		}
		switch stopID {
		case "S01", "S03", "S04": // Franklin Av shuttle stops
			return false
		}
		return true
	case "GS", "H", "FS":
		return false
	default:
		if len(routeID) == 1 && routeID[0] >= 'A' && routeID[0] <= 'Z' {
			re := regexp.MustCompile("^" + routeID + `[0-9]{2}$`)
			return re.MatchString(stopID)
		}
		if len(routeID) == 1 && routeID[0] >= '1' && routeID[0] <= '9' {
			re := regexp.MustCompile("^" + routeID + `[0-9]{2}$`)
			return re.MatchString(stopID)
		}
	}
	return false
}

func stopSortKey(stopID string) int {
	prefix := strings.TrimRight(stopID, "NS")
	if n, err := strconv.Atoi(prefix); err == nil {
		return n
	}
	if len(prefix) >= 2 {
		letter := prefix[0]
		num, _ := strconv.Atoi(prefix[1:])
		return int(letter)*1000 + num
	}
	return 0
}

func (idx *StationIndex) Name(stopID string) string {
	if name, ok := idx.names[stopID]; ok {
		return name
	}
	parent := parentStopID(stopID)
	if name, ok := idx.names[parent]; ok {
		return name
	}
	return stopID
}

func (idx *StationIndex) RouteStations(routeID string) []Station {
	return idx.routeStops[routeID]
}

func (idx *StationIndex) ParentOrder(stopID string) int {
	parent := parentStopID(stopID)
	if order, ok := idx.parentOrder[parent]; ok {
		return order
	}
	return 9999
}

func parentStopID(stopID string) string {
	if strings.HasSuffix(stopID, "N") || strings.HasSuffix(stopID, "S") {
		return strings.TrimSuffix(strings.TrimSuffix(stopID, "N"), "S")
	}
	return stopID
}
