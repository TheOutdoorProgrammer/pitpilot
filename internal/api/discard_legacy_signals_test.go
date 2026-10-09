package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
)

func TestDiscardPolicyConflictAndLatestRevision(t *testing.T) {
	store, h := fixture(t)
	vehicle := vehicle(t, h)
	ctx := context.Background()
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	batch := garage.SignalBatch{Source: "lubelogger", BatchID: "legacy", Observations: []garage.SignalObservation{{Key: "sample", Metric: "speed_kph", Unit: "km/h", Statistic: "sample", Quality: "measured", ObservedAt: &at, Value: 25}}}
	raw, _ := json.Marshal(batch)
	path := "/api/v1/vehicles/" + vehicle.ID + "/signals"
	if w := request(h, "POST", path, string(raw)); w.Code != 200 {
		t.Fatal("fixture ingestion failed")
	}
	preview, err := store.DiscardLegacySignals(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DiscardLegacySignals(ctx, preview.PreviewToken); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "POST", path, string(raw)); w.Code != 409 {
		t.Fatalf("LL replay status: %d", w.Code)
	}
	recovery := garage.ReceiverRecoveryRequest{VehicleID: vehicle.ID, Snapshot: receiverhistory.Snapshot{DeviceID: "receiver", SHA256: strings.Repeat("a", 64), Events: []json.RawMessage{json.RawMessage(`{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"observed_at":"2026-09-24T00:00:00Z","readings":{"speed_kph":25},"dtcs":[]}`)}}}
	raw, _ = json.Marshal(recovery)
	if w := receiverAPIRequest(h, "POST", "/api/v1/migrations/receiver/preview", string(raw)); w.Code != 409 {
		t.Fatalf("receiver replay status: %d", w.Code)
	}
	w := request(h, "GET", path+"/latest", "")
	var latest garage.LatestSignals
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &latest) != nil || len(latest.HistoryRevision) != 64 || len(latest.Series) != 0 {
		t.Fatal("latest response missing clean revision")
	}
	batch.Source = "pi"
	batch.BatchID = "native"
	raw, _ = json.Marshal(batch)
	if w = request(h, "POST", path, string(raw)); w.Code != 200 {
		t.Fatalf("native ingestion blocked: %d", w.Code)
	}
}
