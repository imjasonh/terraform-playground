package mta

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAlertUnmarshalMercuryExtensions(t *testing.T) {
	raw := `{
		"activePeriod": [{"start": "1609459200"}],
		"informedEntity": [{
			"agencyId": "MTASBWY",
			"routeId": "4",
			".nyctMercuryEntitySelector": {"sortOrder": "MTASBWY:4:26"}
		}],
		".nyctMercuryAlert": {
			"alertType": "Delays",
			"displayBeforeActive": 0
		}
	}`

	var alert Alert
	if err := json.Unmarshal([]byte(raw), &alert); err != nil {
		t.Fatal(err)
	}

	if got := alert.MercuryAlert.AlertType; got != "Delays" {
		t.Fatalf("alertType: got %q", got)
	}
	if alert.MercuryAlert.DisplayBeforeActive == nil || *alert.MercuryAlert.DisplayBeforeActive != 0 {
		t.Fatalf("displayBeforeActive not parsed")
	}
	if len(alert.InformedEntity) != 1 {
		t.Fatalf("informedEntity len: got %d", len(alert.InformedEntity))
	}
	entity := alert.InformedEntity[0]
	if entity.RouteID != "4" || entity.AgencyID != "MTASBWY" {
		t.Fatalf("entity: %+v", entity)
	}
	if entity.MercuryEntitySelector.SortOrder != "MTASBWY:4:26" {
		t.Fatalf("sortOrder: got %q", entity.MercuryEntitySelector.SortOrder)
	}
}

func TestFeedUnmarshalFromFixture(t *testing.T) {
	data := readTestFile(t, "sample-alerts.json")
	var feed Feed
	if err := json.Unmarshal(data, &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Entity) == 0 {
		t.Fatal("expected entities")
	}
	if feed.Entity[0].Alert == nil {
		t.Fatal("expected alert on first entity")
	}
}

func readTestFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(testdataPath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
