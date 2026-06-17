package mta

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

const (
	StationTrainHere    = "train"
	StationArrivingSoon = "arriving"
)

type StationActivity struct {
	StopID      string
	StationName string
	State       string
	Detail      string
	ETA         time.Time
	SortOrder   int
}

type TripClient struct {
	HTTPClient *http.Client
	FeedURL    string // optional override for entire client (file:// fixture)
}

func NewTripClient() *TripClient {
	return &TripClient{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *TripClient) FetchRoute(ctx context.Context, routeID string) (*gtfs.FeedMessage, error) {
	url := c.FeedURL
	if url == "" {
		url = os.Getenv("MTA_TRIP_FEED_URL")
	}
	if url == "" {
		url = TripFeedURL(routeID)
	}

	var data []byte
	var err error
	if strings.HasPrefix(url, "file://") {
		path := strings.TrimPrefix(url, "file://")
		data, err = os.ReadFile(path)
	} else {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("User-Agent", "mta-ssh/1.0")
		resp, doErr := c.HTTPClient.Do(req)
		if doErr != nil {
			return nil, fmt.Errorf("fetch trip feed: %w", doErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("fetch trip feed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		data, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		return nil, err
	}

	feed := &gtfs.FeedMessage{}
	if err := proto.Unmarshal(data, feed); err != nil {
		return nil, fmt.Errorf("decode trip feed: %w", err)
	}
	return feed, nil
}

func StationActivityForRoute(feed *gtfs.FeedMessage, routeID string, now time.Time) ([]StationActivity, error) {
	idx, err := Stations()
	if err != nil {
		return nil, err
	}

	arrivingSoon := 5 * time.Minute
	type agg struct {
		state     string
		detail    string
		eta       time.Time
		sortOrder int
	}
	byParent := make(map[string]*agg)

	record := func(stopID, state, detail string, eta time.Time) {
		parent := parentStopID(stopID)
		order := idx.ParentOrder(stopID)
		cur, ok := byParent[parent]
		if !ok {
			byParent[parent] = &agg{state: state, detail: detail, eta: eta, sortOrder: order}
			return
		}
		if statePriority(state) > statePriority(cur.state) {
			cur.state = state
			cur.detail = detail
			cur.eta = eta
		}
		if order < cur.sortOrder {
			cur.sortOrder = order
		}
	}

	for _, entity := range feed.Entity {
		if vp := entity.GetVehicle(); vp != nil {
			trip := vp.GetTrip()
			if trip == nil || trip.GetRouteId() != routeID {
				continue
			}
			stopID := vp.GetStopId()
			if stopID == "" {
				continue
			}
			switch vp.GetCurrentStatus() {
			case gtfs.VehiclePosition_STOPPED_AT:
				record(stopID, StationTrainHere, "Train at platform", now)
			case gtfs.VehiclePosition_INCOMING_AT:
				record(stopID, StationArrivingSoon, "Train arriving", now)
			}
		}

		if tu := entity.GetTripUpdate(); tu != nil {
			trip := tu.GetTrip()
			if trip == nil || trip.GetRouteId() != routeID {
				continue
			}
			for _, stu := range tu.GetStopTimeUpdate() {
				stopID := stu.GetStopId()
				if stopID == "" {
					continue
				}
				arr := eventTime(stu.GetArrival(), stu.GetDeparture())
				if arr.IsZero() {
					continue
				}
				delta := arr.Sub(now)
				switch {
				case delta <= 45*time.Second && delta >= -2*time.Minute:
					record(stopID, StationTrainHere, "Train at platform", arr)
				case delta > 0 && delta <= arrivingSoon:
					record(stopID, StationArrivingSoon, fmt.Sprintf("Arriving in %s", formatDuration(delta)), arr)
				}
			}
		}
	}

	routeStations := idx.RouteStations(routeID)
	out := make([]StationActivity, 0, len(routeStations))
	seen := make(map[string]bool)

	for _, st := range routeStations {
		if agg, ok := byParent[st.ID]; ok {
			out = append(out, StationActivity{
				StopID:      st.ID,
				StationName: st.Name,
				State:       agg.state,
				Detail:      agg.detail,
				ETA:         agg.eta,
				SortOrder:   agg.sortOrder,
			})
			seen[st.ID] = true
		}
	}

	// Include any active stop not in the static list (detours, etc.)
	for parent, agg := range byParent {
		if seen[parent] {
			continue
		}
		out = append(out, StationActivity{
			StopID:      parent,
			StationName: idx.Name(parent),
			State:       agg.state,
			Detail:      agg.detail,
			ETA:         agg.eta,
			SortOrder:   agg.sortOrder,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].StationName < out[j].StationName
	})

	return out, nil
}

func statePriority(state string) int {
	switch state {
	case StationTrainHere:
		return 2
	case StationArrivingSoon:
		return 1
	default:
		return 0
	}
}

func eventTime(arrival, departure *gtfs.TripUpdate_StopTimeEvent) time.Time {
	if arrival != nil && arrival.Time != nil {
		return time.Unix(int64(*arrival.Time), 0)
	}
	if departure != nil && departure.Time != nil {
		return time.Unix(int64(*departure.Time), 0)
	}
	return time.Time{}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()+0.5))
}
