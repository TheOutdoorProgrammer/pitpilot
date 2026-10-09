package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestGPSConfigNegotiationPreservesOldUpdaterContract(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	ctx := context.Background()
	e, err := s.CreateDevice(ctx, v.ID, "GPS fixture")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(ctx, e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	patch := request(h, "PATCH", "/api/v1/devices/"+d.DeviceID, `{"gpsRecording":true}`)
	if patch.Code != 200 {
		t.Fatal("GPS opt-in rejected", patch.Code)
	}
	for _, capability := range []string{"", "gps-v1"} {
		req := httptest.NewRequest("GET", "/api/v1/device/config", nil)
		req.Header.Set("Authorization", "Bearer "+d.Token)
		req.Header.Set("X-PitPilot-Capabilities", capability)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		_, present := fields["gpsRecording"]
		if present != (capability != "") {
			t.Fatal("GPS field negotiation failed")
		}
		if capability == "" {
			var old struct {
				DeviceID        string `json:"deviceId"`
				VehicleID       string `json:"vehicleId"`
				AutoUpdate      bool   `json:"autoUpdate"`
				ProtocolVersion int    `json:"protocolVersion"`
			}
			decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&old); err != nil {
				t.Fatal("old automatic updater stranded", err)
			}
		}
	}
	if w := request(h, "PATCH", "/api/v1/devices/"+d.DeviceID, `{"gpsRecording":false,"autoUpdate":false}`); w.Code != 200 {
		t.Fatal("sparse combined policy rejected", w.Code)
	}
	if config, err := s.DeviceConfiguration(ctx, d.Token); err != nil || config.GPSRecording || config.AutoUpdate {
		t.Fatal("policy was not persisted", err)
	}
}

func TestGPSLocationAuthZeroFixesAndDeletion(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	route := "/api/v1/vehicles/" + v.ID
	w := request(h, "GET", route+"/location", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"location":null}` {
		t.Fatal("invented location")
	}
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest("GET", route+"/location", nil))
	if unauthorized.Code != 401 {
		t.Fatal("location is public")
	}
	e, err := s.CreateDevice(context.Background(), v.ID, "GPS fixture")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(context.Background(), e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	for i := 0; i < 2; i++ {
		hdop := 0.9
		quality := 1
		satellites := 8
		speed := 40.0
		when := at.Add(time.Duration(i*10) * time.Second)
		batch := garage.SignalBatch{Source: "pi", BatchID: garage.NewID(), Contexts: []garage.SignalContext{{Key: garage.NewID(), Kind: "location", ObservedAt: &when, Location: &garage.SignalLocation{Type: "gps", RecordingID: "fixture", Latitude: float64(i) * 0.001, Longitude: 0, HDOP: &hdop, FixQuality: &quality, Satellites: &satellites, SpeedKPH: &speed}}}}
		raw, _ := json.Marshal(batch)
		w = deviceAPIRequest(h, "POST", "/api/v1/device/signals", d.Token, string(raw))
		if w.Code != 200 {
			t.Fatal("GPS upload failed", w.Code)
		}
	}
	w = request(h, "GET", route+"/location", "")
	var result struct {
		Location *garage.RecordedLocation `json:"location"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Location == nil || result.Location.Longitude != 0 || result.Location.Source != "pi-gps" || result.Location.AccuracyMeters != nil {
		t.Fatal("incorrect GPS response", err)
	}
	w = request(h, "GET", route+"/trips?limit=1", "")
	var trips []garage.Trip
	if err = json.Unmarshal(w.Body.Bytes(), &trips); err != nil || len(trips) != 1 {
		t.Fatal("automatic trip missing", err)
	}
	if w = request(h, "GET", route+"/trips?limit=101", ""); w.Code != 400 {
		t.Fatal("unbounded list")
	}
	if w = request(h, "GET", route+"/trips?before=bad", ""); w.Code != 400 {
		t.Fatal("invalid cursor accepted")
	}
	if w = request(h, "DELETE", route+"/location-history", ""); w.Code != 204 {
		t.Fatal("clear failed", w.Code)
	}
	w = request(h, "GET", route+"/location", "")
	if strings.TrimSpace(w.Body.String()) != `{"location":null}` {
		t.Fatal("last-known location survived clear")
	}
	w = request(h, "GET", route+"/trips", "")
	if strings.TrimSpace(w.Body.String()) != `[]` {
		t.Fatal("route survived clear")
	}
}
