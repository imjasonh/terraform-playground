package mta

import (
	"encoding/json"
	"strings"
)

const DefaultFeedURL = "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/camsys%2Fsubway-alerts.json"

type Feed struct {
	Header Header  `json:"header"`
	Entity []Entry `json:"entity"`
}

type Header struct {
	Timestamp json.Number `json:"timestamp"`
}

type Entry struct {
	ID    string `json:"id"`
	Alert *Alert `json:"alert"`
}

type Alert struct {
	ActivePeriod   []ActivePeriod   `json:"activePeriod"`
	InformedEntity []InformedEntity `json:"informedEntity"`
	HeaderText     *TranslatedText  `json:"headerText"`
	MercuryAlert   MercuryAlert     `json:"-"`
}

type ActivePeriod struct {
	Start json.Number `json:"start"`
	End   json.Number `json:"end"`
}

type InformedEntity struct {
	AgencyID              string `json:"agencyId"`
	RouteID               string `json:"routeId"`
	MercuryEntitySelector MercuryEntitySelector
}

type MercuryEntitySelector struct {
	SortOrder string `json:"sortOrder"`
}

type MercuryAlert struct {
	AlertType           string `json:"alertType"`
	DisplayBeforeActive *int   `json:"displayBeforeActive"`
}

type TranslatedText struct {
	Translation []struct {
		Text string `json:"text"`
	} `json:"translation"`
}

func (a *Alert) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if v, ok := raw["activePeriod"]; ok {
		_ = json.Unmarshal(v, &a.ActivePeriod)
	}
	if v, ok := raw["headerText"]; ok {
		_ = json.Unmarshal(v, &a.HeaderText)
	}
	if v, ok := raw["informedEntity"]; ok {
		var entities []map[string]json.RawMessage
		if err := json.Unmarshal(v, &entities); err == nil {
			a.InformedEntity = make([]InformedEntity, 0, len(entities))
			for _, entityRaw := range entities {
				var entity InformedEntity
				for key, value := range entityRaw {
					lower := strings.ToLower(key)
					switch {
					case key == "agencyId":
						_ = json.Unmarshal(value, &entity.AgencyID)
					case key == "routeId":
						_ = json.Unmarshal(value, &entity.RouteID)
					case strings.Contains(lower, "mercuryentityselector"):
						_ = json.Unmarshal(value, &entity.MercuryEntitySelector)
					}
				}
				a.InformedEntity = append(a.InformedEntity, entity)
			}
		}
	}

	for key, value := range raw {
		if strings.Contains(strings.ToLower(key), "mercuryalert") {
			_ = json.Unmarshal(value, &a.MercuryAlert)
		}
	}

	return nil
}

func numberInt64(n json.Number) int64 {
	v, _ := n.Int64()
	return v
}
