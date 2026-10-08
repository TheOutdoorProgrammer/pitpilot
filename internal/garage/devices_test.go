package garage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeviceEnrollmentIsOneTimeAndExpires(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	enrollment, err := s.CreateDevice(ctx, id, "Garage Pi")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := json.Marshal(enrollment.Device)
	if err != nil || strings.Contains(string(pending), "queuedBatches") {
		t.Fatal("pending pairing invented collector status", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.EnrollDevice(ctx, enrollment.EnrollmentToken)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if !errors.Is(err, ErrDeviceUnauthorized) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("enrollment succeeded %d times", success)
	}
	second, err := s.CreateDevice(ctx, id, "Expired Pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE devices SET enrollment_expires=0 WHERE id=?", second.Device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnrollDevice(ctx, second.EnrollmentToken); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatal("expired enrollment accepted", err)
	}
}

func TestDeviceScopeRetriesRevocationAndPrivateState(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	e1, err := s.CreateDevice(ctx, id, "One")
	if err != nil {
		t.Fatal(err)
	}
	d1, err := s.EnrollDevice(ctx, e1.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.CreateDevice(ctx, id, "Two")
	if err != nil {
		t.Fatal(err)
	}
	d2, err := s.EnrollDevice(ctx, e2.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	batch := SignalBatch{Source: "smartcar", BatchID: "same-batch", Observations: []SignalObservation{signalSample("same-key", 42, time.Now().UTC())}}
	for _, token := range []string{d1.Token, d2.Token} {
		r, err := s.IngestDeviceSignals(ctx, token, batch)
		if err != nil || r.Created != 1 {
			t.Fatal("isolated ingest", r, err)
		}
		r, err = s.IngestDeviceSignals(ctx, token, batch)
		if err != nil || r.Skipped != 1 {
			t.Fatal("retry", r, err)
		}
	}
	if batch.Observations[0].Key != "same-key" {
		t.Fatal("mutated caller's batch")
	}
	batch.Observations[0].Value = 50
	if _, err = s.IngestDeviceSignals(ctx, d1.Token, batch); !errors.Is(err, ErrSignalConflict) {
		t.Fatal("conflicting retry accepted", err)
	}
	h := DeviceHeartbeat{Version: "0.4.0", CollectionState: "waiting_clock", UpdateState: "healthy", QueuedBatches: 2, RejectedSamples: 3}
	if err = s.HeartbeatDevice(ctx, d1.Token, h); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetDeviceAutoUpdate(ctx, d1.DeviceID, false); err != nil {
		t.Fatal(err)
	}
	config, err := s.DeviceConfiguration(ctx, d1.Token)
	if err != nil || config.AutoUpdate || config.VehicleID != id || config.ProtocolVersion != 1 {
		t.Fatal(config, err)
	}
	if err = s.RevokeDevice(ctx, d1.DeviceID); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeDevice(ctx, d1.DeviceID); err != nil {
		t.Fatal("idempotent revoke", err)
	}
	if _, err = s.DeviceConfiguration(ctx, d1.Token); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatal("revoked config", err)
	}
	if err = s.HeartbeatDevice(ctx, d1.Token, h); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatal("revoked heartbeat", err)
	}
	batch.BatchID = "after-revoke"
	if _, err = s.IngestDeviceSignals(ctx, d1.Token, batch); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatal("revoked ingest", err)
	}
	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Signals) != 2 {
		t.Fatal("revocation changed observations")
	}
	for _, signal := range exported.Signals {
		if signal.Source != "pi" || !strings.HasPrefix(signal.Observation.Key, d1.DeviceID+":") && !strings.HasPrefix(signal.Observation.Key, d2.DeviceID+":") {
			t.Fatal("source/identity not bound to device")
		}
	}
	raw, _ := json.Marshal(exported)
	devices, err := s.Devices(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(devices)
	for _, secret := range []string{e1.EnrollmentToken, d1.Token, e2.EnrollmentToken, d2.Token, deviceTokenHash(d2.Token)} {
		if strings.Contains(string(raw), secret) || strings.Contains(string(public), secret) {
			t.Fatal("credential escaped private storage")
		}
	}
	if devices[0].LastSeenAt == nil || devices[0].RevokedAt == nil || devices[0].CollectionState != "waiting_clock" {
		t.Fatal("missing truthful status")
	}
	var tokenHash string
	if err = s.db.QueryRow("SELECT token_hash FROM devices WHERE id=?", d2.DeviceID).Scan(&tokenHash); err != nil || tokenHash != deviceTokenHash(d2.Token) {
		t.Fatal("unhashed credential storage", err)
	}
}

func TestHeartbeatRestartPreservesConfirmedTimes(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	enrollment, err := s.CreateDevice(ctx, id, "Restarting Pi")
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.EnrollDevice(ctx, enrollment.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Now().UTC().Add(-time.Minute)
	uploaded := observed.Add(time.Second)
	h := DeviceHeartbeat{Version: "0.4.0", CollectionState: "collecting", UpdateState: "idle", LastObservedAt: &observed, LastUploadAt: &uploaded}
	if err = s.HeartbeatDevice(ctx, device.Token, h); err != nil {
		t.Fatal(err)
	}
	older := observed.Add(-time.Minute)
	for _, timestamp := range []*time.Time{nil, &older} {
		h.LastObservedAt, h.LastUploadAt = timestamp, timestamp
		h.CollectionState = "starting"
		if err = s.HeartbeatDevice(ctx, device.Token, h); err != nil {
			t.Fatal(err)
		}
		devices, err := s.Devices(ctx, id)
		if err != nil || len(devices) != 1 {
			t.Fatal("read collector status", err)
		}
		got := devices[0]
		if got.LastObservedAt == nil || !got.LastObservedAt.Equal(observed) || got.LastUploadAt == nil || !got.LastUploadAt.Equal(uploaded) || got.CollectionState != "starting" {
			t.Fatal("restart erased confirmed times or failed to update current state")
		}
	}
}

func TestVehicleDeletionRevokesDeviceAndPendingEnrollment(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	e, err := s.CreateDevice(ctx, id, "Pi")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(ctx, e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.CreateDevice(ctx, id, "Pending")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteVehicle(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeviceConfiguration(ctx, d.Token); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatal(err)
	}
	if _, err = s.EnrollDevice(ctx, pending.EnrollmentToken); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatal(err)
	}
}
