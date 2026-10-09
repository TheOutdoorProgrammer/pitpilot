package smartcar

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestLocationOnlySyncPreservesOEMTimeAndOtherVehicleTrips(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	signals := []remoteSignal{fixtureSignal("location-preciselocation", `{"latitude":0,"longitude":0,"locationType":"LAST_PARKED"}`, at.Format(time.RFC3339Nano))}
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": signals, "meta": map[string]int{"totalCount": len(signals)}})
	})
	adoptFixture(t, s, vehicle)

	pi := garage.Vehicle{ID: garage.NewID(), Name: "Pi vehicle", CreatedAt: at}
	if err := store.CreateVehicle(ctx, pi); err != nil {
		t.Fatal(err)
	}
	hdop, speed, quality, satellites := 0.9, 40.0, 1, 8
	batch := garage.SignalBatch{Source: "pi", BatchID: "pi-drive"}
	for i := 0; i < 2; i++ {
		when := at.Add(time.Duration(i*10) * time.Second)
		batch.Contexts = append(batch.Contexts, garage.SignalContext{Key: garage.NewID(), Kind: "location", ObservedAt: &when, Location: &garage.SignalLocation{Type: "gps", RecordingID: "drive", Latitude: 1 + float64(i)*0.001, Longitude: 1, HDOP: &hdop, SpeedKPH: &speed, FixQuality: &quality, Satellites: &satellites}})
	}
	if _, err := store.IngestSignals(ctx, pi.ID, batch); err != nil {
		t.Fatal(err)
	}
	before, err := store.Trips(ctx, pi.ID, 50, time.Time{}, "")
	if err != nil || len(before) != 1 {
		t.Fatal("Pi trip fixture failed", err)
	}
	beforeJSON, _ := json.Marshal(before)
	run := func() {
		t.Helper()
		claimed, err := store.ClaimSmartcar(ctx, time.Now().Add(2*time.Hour), s.interval)
		if err != nil {
			t.Fatal(err)
		}
		s.process(ctx, claimed)
	}
	assertLocation := func(wantType string, wantTime time.Time, wantLatitude float64) {
		t.Helper()
		location, err := store.LatestLocation(ctx, vehicle)
		if err != nil || location == nil || location.Source != "smartcar" || location.LocationType != wantType || !location.RecordedAt.Equal(wantTime) || location.Latitude != wantLatitude {
			t.Fatalf("incorrect last-known location: %+v, %v", location, err)
		}
		trips, err := store.Trips(ctx, vehicle, 50, time.Time{}, "")
		if err != nil || len(trips) != 0 {
			t.Fatal("Smartcar position fabricated a trip", err)
		}
	}
	run()
	run()
	assertLocation("LAST_PARKED", at, 0)
	status, err := s.Status(ctx, vehicle)
	if err != nil || status.State != "connected" || status.LastSuccessAt == nil || status.LatestObservedAt == nil || !status.LatestObservedAt.Equal(at) {
		t.Fatal("location-only response was not a successful sync", err)
	}
	export, err := store.Export(ctx)
	if err != nil || len(export.SignalContexts) != 3 {
		t.Fatal("location retry produced duplicates", err)
	}

	newer := at.Add(time.Hour)
	signals = []remoteSignal{fixtureSignal("location-preciselocation", `{"latitude":0.01,"longitude":0,"locationType":"CURRENT"}`, newer.Format(time.RFC3339Nano))}
	run()
	assertLocation("CURRENT", newer, 0.01)
	signals = []remoteSignal{fixtureSignal("location-preciselocation", `{"latitude":0.02,"longitude":0,"locationType":"LAST_PARKED"}`, at.Add(-time.Hour).Format(time.RFC3339Nano))}
	run()
	assertLocation("CURRENT", newer, 0.01)
	// Missing, malformed and unavailable updates must retain the last valid fix.
	unavailable := signals[0]
	unavailable.Attributes.Status.Value = "ERROR"
	for _, invalid := range [][]remoteSignal{
		{},
		{fixtureSignal("location-preciselocation", `{"latitude":0.03,"longitude":0,"locationType":"CURRENT"}`, "")},
		{unavailable},
	} {
		signals = invalid
		run()
		assertLocation("CURRENT", newer, 0.01)
	}
	after, err := store.Trips(ctx, pi.ID, 50, time.Time{}, "")
	afterJSON, _ := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatal("Smartcar sync changed the other vehicle's trip", err)
	}
	location, err := store.LatestLocation(ctx, pi.ID)
	if err != nil || location == nil || location.Source != "pi-gps" || location.LocationType != "gps" || location.Longitude != 1 {
		t.Fatal("Smartcar sync changed the other vehicle's location", err)
	}
}
