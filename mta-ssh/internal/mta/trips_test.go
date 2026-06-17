package mta

import (
	"os"
	"path/filepath"
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

func TestWriteSampleTripFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "sample-trips.pb")
	now := time.Unix(1_700_000_000, 0)
	data, err := proto.Marshal(sampleTripFeed(now))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

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
