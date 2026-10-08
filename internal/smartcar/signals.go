package smartcar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
)

type mapping struct{ metric, from, unit, quality string }

var numericSignals = map[string]mapping{
	"internalcombustionengine-fuellevel":       {"fuel_level_pct", "percent", "%", "measured"},
	"internalcombustionengine-amountremaining": {"fuel_remaining_l", "liters", "L", "measured"},
	"internalcombustionengine-oillife":         {"oil_life_pct", "percent", "%", "measured"},
	"internalcombustionengine-range":           {"range_km", "km", "km", "estimated"},
	"odometer-traveleddistance":                {"odometer_km", "km", "km", "measured"},
	"motion-currentspeed":                      {"speed_kph", "km/h", "km/h", "measured"},
}

func supportedCodes() []string {
	out := []string{"location-preciselocation", "wheel-tires"}
	for k := range numericSignals {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func fingerprint(parts ...string) string {
	h := sha256.New()
	for _, v := range parts {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type adapted struct {
	Batch                    garage.SignalBatch
	Metrics                  []string
	Unavailable, Unsupported int
	Latest                   *time.Time
	Reconnect                bool
}

func adapt(vehicle string, signals []remoteSignal, now time.Time) adapted {
	r := adapted{Batch: garage.SignalBatch{Source: "smartcar", Observations: []garage.SignalObservation{}}, Metrics: []string{}}
	metrics := map[string]bool{}
	seen := map[string]bool{}
	for _, signal := range signals {
		code := signal.Attributes.Code
		m, numeric := numericSignals[code]
		if !numeric && code != "location-preciselocation" && code != "wheel-tires" {
			r.Unsupported++
			continue
		}
		if signal.Attributes.Status.Value != "SUCCESS" {
			r.Unavailable++
			if e := signal.Attributes.Status.Error; e != nil && (e.Code == "AUTHENTICATION_FAILED" || e.Code == "PERMISSION_DENIED") {
				r.Reconnect = true
			}
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, signal.Meta.OEMUpdatedAt)
		if err != nil || at.Year() < 2000 || at.After(now.Add(5*time.Minute)) || jsonutil.Validate(signal.Attributes.Body) != nil || seen[code] {
			r.Unavailable++
			continue
		}
		seen[code] = true
		at = at.UTC()
		key := fingerprint(vehicle, code, at.Format(time.RFC3339Nano))
		added := false
		if numeric {
			var body struct {
				Value *float64 `json:"value"`
				Unit  string   `json:"unit"`
			}
			if json.Unmarshal(signal.Attributes.Body, &body) != nil || body.Value == nil || body.Unit != m.from {
				r.Unavailable++
				continue
			}
			o := garage.SignalObservation{Key: key, Metric: m.metric, Unit: m.unit, Statistic: "sample", Quality: m.quality, Value: *body.Value, ObservedAt: &at}
			if o.Validate() != nil {
				r.Unavailable++
				continue
			}
			r.Batch.Observations = append(r.Batch.Observations, o)
			metrics[m.metric] = true
			added = true
		} else if code == "wheel-tires" {
			var body struct {
				Rows    *int   `json:"rowCount"`
				Columns *int   `json:"columnCount"`
				Unit    string `json:"unit"`
				Values  []struct {
					Row      *int     `json:"row"`
					Column   *int     `json:"column"`
					Pressure *float64 `json:"tirePressure"`
				} `json:"values"`
			}
			if json.Unmarshal(signal.Attributes.Body, &body) != nil || body.Rows == nil || body.Columns == nil || *body.Rows != 2 || *body.Columns != 2 || body.Unit != "kPa" || len(body.Values) > 4 || len(body.Values) == 0 {
				r.Unavailable++
				continue
			}
			positions := map[int]bool{}
			tires := []garage.SignalObservation{}
			valid := true
			for _, v := range body.Values {
				if v.Row == nil || v.Column == nil || v.Pressure == nil || *v.Row < 0 || *v.Row > 1 || *v.Column < 0 || *v.Column > 1 {
					valid = false
					break
				}
				position := *v.Row*2 + *v.Column
				if positions[position] {
					valid = false
					break
				}
				positions[position] = true
				metric := []string{"tire_fl_kpa", "tire_fr_kpa", "tire_rl_kpa", "tire_rr_kpa"}[position]
				o := garage.SignalObservation{Key: fingerprint(key, metric), Metric: metric, Unit: "kPa", Statistic: "sample", Quality: "measured", Value: *v.Pressure, ObservedAt: &at}
				if o.Validate() != nil {
					valid = false
					break
				}
				tires = append(tires, o)
			}
			if !valid {
				r.Unavailable++
				continue
			}
			for _, o := range tires {
				r.Batch.Observations = append(r.Batch.Observations, o)
				metrics[o.Metric] = true
			}
			added = true
		} else {
			var body struct {
				Latitude  *float64 `json:"latitude"`
				Longitude *float64 `json:"longitude"`
				Type      string   `json:"locationType"`
			}
			if json.Unmarshal(signal.Attributes.Body, &body) != nil || body.Latitude == nil || body.Longitude == nil {
				r.Unavailable++
				continue
			}
			if math.IsNaN(*body.Latitude) || math.IsNaN(*body.Longitude) {
				r.Unavailable++
				continue
			}
			c := garage.SignalContext{Key: key, Kind: "location", ObservedAt: &at, Location: &garage.SignalLocation{Latitude: *body.Latitude, Longitude: *body.Longitude, Type: body.Type}}
			if c.Validate() != nil {
				r.Unavailable++
				continue
			}
			r.Batch.Contexts = append(r.Batch.Contexts, c)
			added = true
		}
		if added && (r.Latest == nil || at.After(*r.Latest)) {
			latest := at
			r.Latest = &latest
		}
	}
	for metric := range metrics {
		r.Metrics = append(r.Metrics, metric)
	}
	sort.Strings(r.Metrics)
	sort.Slice(r.Batch.Observations, func(i, j int) bool { return r.Batch.Observations[i].Key < r.Batch.Observations[j].Key })
	sort.Slice(r.Batch.Contexts, func(i, j int) bool { return r.Batch.Contexts[i].Key < r.Batch.Contexts[j].Key })
	raw, _ := json.Marshal(r.Batch)
	r.Batch.BatchID = fingerprint(string(raw))
	return r
}
