package picollector

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/gps"
)

func TestPowerGuardUsesFreshFixesAndHysteresis(t *testing.T) {
	q, err := OpenQueue(t.TempDir(), "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	g := newPowerGuard(&garage.PowerSafeZone{Latitude: 0, Longitude: 0, RadiusMeters: 152.4})
	now := time.Now().UTC()
	observe := func(meters float64, elapsed time.Duration) {
		now = now.Add(elapsed)
		fix := gps.Fix{Latitude: meters / 6371000 * 180 / math.Pi, RecordedAt: now}
		g.observe(context.Background(), q, fix, now)
	}
	observe(153, 0)
	if g.priority(now) {
		t.Fatal("activated outside 500 feet")
	}
	observe(151, time.Second)
	if !g.priority(now) {
		t.Fatal("did not activate inside 500 feet")
	}
	observe(165, time.Second)
	if !g.priority(now) {
		t.Fatal("boundary jitter disabled priority")
	}
	observe(180, time.Second)
	if g.priority(now) {
		t.Fatal("did not leave beyond exit margin")
	}
	observe(0, time.Second)
	if !g.priority(now) || g.priority(now.Add(powerFixLifetime+time.Second)) {
		t.Fatal("stale position remained authoritative")
	}
	g.observe(context.Background(), q, gps.Fix{RecordedAt: now.Add(-time.Minute)}, now.Add(time.Minute))
	if g.priority(now.Add(time.Minute)) {
		t.Fatal("stale fix reactivated priority")
	}
	g.configure(nil)
	observe(0, time.Second)
	if g.priority(now) {
		t.Fatal("disabled policy still active")
	}
}

func TestPowerGuardWakeDoesNotBypassRateLimit(t *testing.T) {
	g := newPowerGuard(&garage.PowerSafeZone{RadiusMeters: 152.4})
	g.inside, g.lastFix = true, time.Now()
	signal(g.entered)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if g.waitForUpload(ctx, time.Minute, false, true) {
		t.Fatal("geofence entry bypassed Retry-After")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if !g.waitForUpload(ctx2, time.Minute, false, false) {
		t.Fatal("arrival did not interrupt ordinary backoff")
	}
	g.pending()
	if !g.waitForUpload(ctx2, time.Minute, true, false) {
		t.Fatal("fresh queued reading did not wake idle uploader")
	}
	// Queue notifications must not turn an offline backoff into a busy loop.
	g.pending()
	ctx3, cancel3 := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel3()
	if g.waitForUpload(ctx3, time.Minute, false, false) {
		t.Fatal("ordinary sample bypassed failed-upload backoff")
	}
}

func TestPowerPolicyPersistsPrivatelyAndSurvivesOldServer(t *testing.T) {
	id := Identity{DeviceID: "pi", VehicleID: "car"}
	zone := &garage.PowerSafeZone{Latitude: 40, Longitude: -75, RadiusMeters: 152.4}
	dir := t.TempDir()
	state := &runtimeState{power: newPowerGuard(nil)}
	response := garage.DevicePowerPolicy{DeviceID: id.DeviceID, VehicleID: id.VehicleID, PowerSafeZone: zone}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/device/power-policy" {
			t.Error("unexpected policy path", r.URL.Path)
		}
		json.NewEncoder(w).Encode(response)
	}))
	client, _ := NewClient(server.URL, "private-token", true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { refreshPowerPolicy(ctx, dir, id, client, state); close(done) }()
	deadline := time.Now().Add(time.Second)
	for !equalPowerZones(state.power.configuredZone(), zone) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	server.Close()
	if !equalPowerZones(readPowerPolicy(dir, id), zone) {
		t.Fatal("policy did not survive private cache reload")
	}
	path := filepath.Join(dir, "power-policy.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("home coordinates are not private", err)
	}
	if readPowerPolicy(dir, Identity{DeviceID: "another", VehicleID: "car"}) != nil || readPowerPolicy(dir, Identity{DeviceID: "pi", VehicleID: "another"}) != nil {
		t.Fatal("policy reused across enrollment scope")
	}
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	client, _ = NewClient(old.URL, "private-token", true)
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	refreshPowerPolicy(ctx, dir, id, client, state)
	if !equalPowerZones(state.power.configuredZone(), zone) || !equalPowerZones(readPowerPolicy(dir, id), zone) {
		t.Fatal("old backend erased offline home policy")
	}
	os.Chmod(path, 0644)
	if readPowerPolicy(dir, id) != nil {
		t.Fatal("accepted publicly readable home policy")
	}
}

func TestPowerGuardQueueSurvivesArrivalAndReopen(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenQueue(dir, "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendGPS(gpsBatch(testGPSFix(), "drive")); err != nil {
		t.Fatal(err)
	}
	g := newPowerGuard(&garage.PowerSafeZone{Latitude: 40, Longitude: -75, RadiusMeters: 152.4})
	fix := testGPSFix()
	fix.RecordedAt = time.Now().UTC()
	g.observe(context.Background(), q, fix, fix.RecordedAt)
	if !g.priority(fix.RecordedAt) {
		t.Fatal("home arrival not recognized")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = OpenQueue(dir, "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if n, rejected, err := q.Status(); err != nil || n != 1 || rejected != 0 {
		t.Fatal("arrival dropped unacknowledged GPS", n, rejected, err)
	}
}

func TestHomeArrivalWakesFailedUploadAndPreservesAcknowledgement(t *testing.T) {
	q, err := OpenQueue(t.TempDir(), "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	batch := testBatch(t, "arrival")
	if err := q.Append(batch); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	failed := make(chan struct{})
	accepted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			close(failed)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"batchId": batch.BatchID, "created": len(batch.Observations), "contextsCreated": len(batch.Contexts)})
		accepted <- struct{}{}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", true)
	if err != nil {
		t.Fatal(err)
	}
	g := newPowerGuard(&garage.PowerSafeZone{Latitude: 40, Longitude: -75, RadiusMeters: 152.4})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { upload(ctx, q, client, &runtimeState{power: g}); close(done) }()
	select {
	case <-failed:
	case <-ctx.Done():
		t.Fatal("initial failed upload did not happen")
	}
	fix := testGPSFix()
	fix.RecordedAt = time.Now().UTC()
	g.observe(context.Background(), q, fix, fix.RecordedAt)
	select {
	case <-accepted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("home arrival left uploader waiting through network backoff")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		n, _, err := q.Status()
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("successful retry did not durably acknowledge the queued batch")
}
