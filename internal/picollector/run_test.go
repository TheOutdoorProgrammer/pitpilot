package picollector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
)

func TestUnsynchronizedClockNeverOpensAdapter(t *testing.T) {
	q, err := OpenQueue(t.TempDir(), "device", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	state := &runtimeState{}
	collect(ctx, Config{PollSeconds: 5, TimeSyncMarker: filepath.Join(t.TempDir(), "absent")}, q, state, func(context.Context) (Sampler, error) {
		t.Fatal("opened adapter before clock synchronization")
		return nil, nil
	})
	if s, _ := state.state(); s != "waiting_clock" {
		t.Fatal(s)
	}
	if n, _, _ := q.Status(); n != 0 {
		t.Fatal("fabricated boot timestamp")
	}
}

type blockingSampler struct {
	closed  atomic.Bool
	started chan struct{}
}

func (s *blockingSampler) SampleDetails(ctx context.Context) (obd.Observation, error) {
	if s.started != nil {
		close(s.started)
	}
	<-ctx.Done()
	return obd.Observation{}, ctx.Err()
}
func (s *blockingSampler) Close() error { s.closed.Store(true); return nil }
func TestRuntimeCancellationClosesAdapterAndPreservesQueue(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(Identity{DeviceID: "pi", VehicleID: "vehicle", Token: strings.Repeat("t", 32)})
	os.WriteFile(filepath.Join(dir, "identity.json"), raw, 0600)
	marker := filepath.Join(dir, "clock")
	os.WriteFile(marker, nil, 0600)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	c := Config{Server: s.URL, StateDirectory: dir, QueueLimit: 10, PollSeconds: 5, TimeSyncMarker: marker, AllowLoopbackHTTP: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := &blockingSampler{started: make(chan struct{})}
	go func() {
		select {
		case <-adapter.started:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := Run(ctx, c, "0.4.0", func(context.Context) (Sampler, error) { return adapter, nil }); err != nil {
		t.Fatal(err)
	}
	if !adapter.closed.Load() {
		t.Fatal("adapter left open")
	}
	q, err := OpenQueue(dir, "pi", 10)
	if err != nil {
		t.Fatal("queue remained locked", err)
	}
	q.Close()
}
func TestReadOnlyReaderBoundsProduceCanonicalSamples(t *testing.T) {
	for _, values := range []map[string]float64{
		{"maf_gps": 12.34, "run_time_s": 3600, "control_voltage_v": 13.5, "fuel_pressure_kpa": 50},
		{"egr_error_pct": -10, "absolute_load_pct": 200, "short_fuel_trim_bank2_pct": -10, "long_fuel_trim_bank2_pct": -10},
		{"evap_pressure_pa": -50, "evap_pressure_wide_pa": -100, "fuel_injection_deg": -5, "commanded_equivalence_ratio": 1},
	} {
		if _, err := Batch(obd.Observation{Readings: values}, time.Now().UTC(), "sample"); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDrainDoesNotNeedAnAdapter(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(Identity{DeviceID: "pi", VehicleID: "vehicle", Token: strings.Repeat("t", 32)})
	os.WriteFile(filepath.Join(dir, "identity.json"), raw, 0600)
	q, err := OpenQueue(dir, "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Append(testBatch(t, "pending")); err != nil {
		t.Fatal(err)
	}
	q.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"batchId": "pending", "created": 1, "updated": 0, "skipped": 0, "contextsCreated": 3, "contextsSkipped": 0})
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Drain(ctx, Config{Server: s.URL, StateDirectory: dir, QueueLimit: 10, AllowLoopbackHTTP: true, SerialPort: "/does-not-exist"}); err != nil {
		t.Fatal(err)
	}
	q, err = OpenQueue(dir, "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	n, _, err := q.Status()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestClockAndAdapterFailureAreDistinct(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "clock")
	os.WriteFile(marker, nil, 0600)
	q, _ := OpenQueue(dir, "pi", 10)
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	state := &runtimeState{}
	collect(ctx, Config{TimeSyncMarker: marker, PollSeconds: 5}, q, state, func(context.Context) (Sampler, error) { return nil, errors.New("not connected") })
	if s, _ := state.state(); s != "adapter_unavailable" {
		t.Fatal(s)
	}
}

type recoveredSampler struct {
	cancel context.CancelFunc
	closed bool
}

func (s *recoveredSampler) SampleDetails(context.Context) (obd.Observation, error) {
	s.cancel()
	return obd.Observation{Readings: map[string]float64{"rpm": 800}}, nil
}

func (s *recoveredSampler) Close() error { s.closed = true; return nil }

func TestAdapterOpenFailureWithTypedNilRetriesAndCollects(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "clock")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	q, err := OpenQueue(dir, "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	adapter := &recoveredSampler{cancel: cancel}
	attempts := 0
	state := &runtimeState{}
	collect(ctx, Config{TimeSyncMarker: marker}, q, state, func(context.Context) (Sampler, error) {
		attempts++
		if attempts == 1 {
			var unavailable *obd.Device
			return unavailable, errors.New("adapter initialization failed")
		}
		return adapter, nil
	})
	if attempts != 2 || !adapter.closed {
		t.Fatalf("attempts=%d closed=%v", attempts, adapter.closed)
	}
	if state, _ := state.state(); state != "collecting" {
		t.Fatalf("state=%s", state)
	}
	if n, _, err := q.Status(); err != nil || n != 1 {
		t.Fatalf("queued=%d error=%v", n, err)
	}
}
