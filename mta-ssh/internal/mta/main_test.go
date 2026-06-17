package mta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	for _, p := range []string{
		filepath.Join("..", "..", "testdata", "gtfs", "stops.txt"),
		filepath.Join("testdata", "gtfs", "stops.txt"),
		"/workspace/mta-ssh/testdata/gtfs/stops.txt",
	} {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			_ = os.Setenv("MTA_GTFS_STOPS", abs)
			break
		}
	}
	os.Exit(m.Run())
}
