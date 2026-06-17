package mta

import (
	"context"
	"os"
	"testing"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

func TestStationActivityForRouteFixture(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	feed := sampleTripFeed(now)
	activities, err := StationActivityForRoute(feed, "L", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) < 2 {
		t.Fatalf("expected at least 2 active stations, got %d", len(activities))
	}

	byName := map[string]StationActivity{}
	for _, a := range activities {
		byName[a.StationName] = a
	}
	if byName["Lorimer St"].State != StationTrainHere {
		t.Fatalf("Lorimer St: got %q", byName["Lorimer St"].State)
	}
	if byName["1 Av"].State != StationArrivingSoon {
		t.Fatalf("1 Av: got %q", byName["1 Av"].State)
	}
}

func TestSampleTripFixtureOnDisk(t *testing.T) {
	path := testdataPath(t, "sample-trips.pb")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	feed := &gtfs.FeedMessage{}
	if err := proto.Unmarshal(data, feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Entity) == 0 {
		t.Fatal("expected entities in committed fixture")
	}
}

func TestTripClientFetchFixture(t *testing.T) {
	client := &TripClient{FeedURL: "file://" + testdataPath(t, "sample-trips.pb")}
	feed, err := client.FetchRoute(context.Background(), "L")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	activities, err := StationActivityForRoute(feed, "L", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) == 0 {
		t.Fatal("expected activities from file fixture")
	}
}

func TestStationActivityIgnoresOtherRoutes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	activities, err := StationActivityForRoute(sampleTripFeed(now), "4", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 {
		t.Fatalf("expected no activities for route 4, got %d", len(activities))
	}
}

func TestStatePriority(t *testing.T) {
	if statePriority(StationTrainHere) <= statePriority(StationArrivingSoon) {
		t.Fatal("train here should outrank arriving soon")
	}
}

func TestFormatDuration(t *testing.T) {
	if got := formatDuration(30 * time.Second); got != "30s" {
		t.Fatalf("got %q", got)
	}
	if got := formatDuration(2 * time.Minute); got != "2m" {
		t.Fatalf("got %q", got)
	}
}

func TestEventTimePrefersArrival(t *testing.T) {
	ts := int64(1700000000)
	arr := &gtfs.TripUpdate_StopTimeEvent{Time: &ts}
	dep := &gtfs.TripUpdate_StopTimeEvent{Time: protoInt64(ts + 60)}
	got := eventTime(arr, dep)
	if got.Unix() != ts {
		t.Fatalf("got %d want %d", got.Unix(), ts)
	}
}

func TestEventTimeUsesDeparture(t *testing.T) {
	ts := int64(1700000060)
	dep := &gtfs.TripUpdate_StopTimeEvent{Time: &ts}
	got := eventTime(nil, dep)
	if got.Unix() != ts {
		t.Fatalf("got %d", got.Unix())
	}
}

func protoInt64(v int64) *int64 { return &v }

func sampleTripFeed(now time.Time) *gtfs.FeedMessage {
	at := func(d time.Duration) *int64 {
		v := now.Add(d).Unix()
		return &v
	}
	stopped := gtfs.VehiclePosition_STOPPED_AT
	incoming := gtfs.VehiclePosition_INCOMING_AT

	return &gtfs.FeedMessage{
		Header: &gtfs.FeedHeader{
			GtfsRealtimeVersion: proto.String("2.0"),
			Timestamp:         proto.Uint64(uint64(now.Unix())),
		},
		Entity: []*gtfs.FeedEntity{
			{
				Id: proto.String("veh1"),
				Vehicle: &gtfs.VehiclePosition{
					Trip:          &gtfs.TripDescriptor{RouteId: proto.String("L")},
					StopId:        proto.String("L10S"),
					CurrentStatus: &stopped,
				},
			},
			{
				Id: proto.String("trip1"),
				TripUpdate: &gtfs.TripUpdate{
					Trip: &gtfs.TripDescriptor{RouteId: proto.String("L")},
					StopTimeUpdate: []*gtfs.TripUpdate_StopTimeUpdate{
						{
							StopId: proto.String("L06S"),
							Arrival: &gtfs.TripUpdate_StopTimeEvent{
								Time: at(2 * time.Minute),
							},
						},
					},
				},
			},
			{
				Id: proto.String("veh2"),
				Vehicle: &gtfs.VehiclePosition{
					Trip:          &gtfs.TripDescriptor{RouteId: proto.String("L")},
					StopId:        proto.String("L06S"),
					CurrentStatus: &incoming,
				},
			},
		},
	}
}
