package picollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/gps"
	"go.bug.st/serial/enumerator"
	bbolt "go.etcd.io/bbolt"
)

func gpsSentence(body string) string {
	var sum byte
	for _, c := range []byte(body) {
		sum ^= c
	}
	return fmt.Sprintf("$%s*%02X\r\n", body, sum)
}
func gpsStream(at time.Time) string {
	return gpsSentence(fmt.Sprintf("GPRMC,%s,A,4000.000,N,07500.000,W,22.4,84.4,%s,,,A", at.Format("150405.00"), at.Format("020106"))) + gpsSentence(fmt.Sprintf("GPGGA,%s,4000.000,N,07500.000,W,1,08,0.9,100.0,M,0.0,M,,", at.Format("150405.00")))
}
func testGPSFix() gps.Fix {
	at := time.Now().UTC().Add(-time.Minute)
	speed := 40.0
	return gps.Fix{Latitude: 40, Longitude: -75, RecordedAt: at, SpeedKPH: &speed, Satellites: 8, HDOP: 0.9, Quality: 1}
}

func TestGPSQueueRetainsIndependentNativeAndLegacyDelivery(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenQueue(dir, "device", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	batch := gpsBatch(testGPSFix(), "gps-session")
	if err = q.AppendGPS(batch); err != nil {
		t.Fatal(err)
	}
	if err = q.db.View(func(tx *bbolt.Tx) error {
		if tx.Bucket(pendingPi).Stats().KeyN != 0 || tx.Bucket(pendingGPS).Stats().KeyN != 1 {
			t.Fatal("old queue reader would see GPS metadata")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.FirstLegacy(); err != nil || ok {
		t.Fatal("GPS invented legacy OBD event", err)
	}
	obdBatch := testBatch(t, "obd")
	legacy := &LegacyEvent{SchemaVersion: 1, ID: "obd", DeviceID: "legacy", BootID: "boot", Readings: map[string]float64{"rpm": 750}}
	if err = q.AppendDelivery(obdBatch, legacy); err != nil {
		t.Fatal(err)
	}
	if err = q.AppendGPS(obdBatch); err == nil {
		t.Fatal("GPS bypass accepted OBD delivery")
	}
	q.Close()
	q, err = OpenQueue(dir, "device", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	p, ok, err := q.First()
	if err != nil || !ok || !p.gps || p.Batch.BatchID != batch.BatchID {
		t.Fatal("GPS did not survive reopening", err)
	}
	if err = q.Ack(p); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := q.Status(); n != 1 {
		t.Fatal("GPS ack removed unrelated OBD")
	}
	p, _, _ = q.First()
	if err = q.Ack(p); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := q.Status(); n != 1 {
		t.Fatal("native OBD ack removed legacy pending")
	}
	l, _, _ := q.FirstLegacy()
	if err = q.AckLegacy(l); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := q.Status(); n != 0 {
		t.Fatal("acknowledged GPS/OBD not removed")
	}
}

func TestGPSStreamBoundsSamplingAndCancellation(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	stream := strings.Repeat("x", 300) + "\n" + gpsStream(at) + gpsStream(at.Add(time.Second)) + gpsStream(at.Add(5*time.Second))
	var accepted []gps.Fix
	err := readGPS(context.Background(), io.NopCloser(strings.NewReader(stream)), GPSConfig{SampleSeconds: 5}, "session", func() bool { return true }, func() bool { return true }, func(f gps.Fix, recording string) error {
		if !strings.HasPrefix(recording, "session:") {
			t.Fatal("missing bounded recording identity")
		}
		accepted = append(accepted, f)
		return nil
	}, func(string) {})
	if !errors.Is(err, io.EOF) || len(accepted) != 2 {
		t.Fatal("sampling/framing incorrect", len(accepted), err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- readGPS(ctx, reader, GPSConfig{SampleSeconds: 5}, "s", func() bool { return true }, func() bool { return true }, func(gps.Fix, string) error { return nil }, func(string) {})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation left GPS reader blocked")
	}
}

func TestGPSDiscoveryAndConfigDoNotSelectArbitrarySerialPorts(t *testing.T) {
	ports := []*enumerator.PortDetails{{Name: "obd", IsUSB: true, VID: "0403", PID: "6001"}, {Name: "gps6", IsUSB: true, VID: "1546", PID: "01a6"}, {Name: "gps7", IsUSB: true, VID: "1546", PID: "01A7"}, {Name: "not-usb", VID: "1546", PID: "01a6"}}
	if got := recognizedGPSPorts(ports); len(got) != 2 || got[0] != "gps6" || got[1] != "gps7" {
		t.Fatal("unsafe USB detection")
	}
	for _, c := range []GPSConfig{{SerialPort: "/etc/passwd", Baud: 9600, SampleSeconds: 5}, {DeviceGlob: "/dev/tty*", Baud: 9600, SampleSeconds: 5}, {SerialPort: "/dev/gps", DeviceGlob: "/dev/serial/by-id/*", Baud: 9600, SampleSeconds: 5}, {SerialPort: "/dev/gps", SampleSeconds: 5}} {
		if c.Validate() == nil {
			t.Fatal("invalid GPS config accepted")
		}
	}
	for _, c := range []GPSConfig{{SerialPort: "/dev/serial/by-id/selected", Baud: 9600, SampleSeconds: 5}, {DeviceGlob: "/dev/serial/by-id/*u-blox*", Baud: 38400, SampleSeconds: 10}} {
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGPSPolicyPersistsPrivatelyWithoutChangingIdentity(t *testing.T) {
	dir := t.TempDir()
	identity := []byte(`{"deviceId":"pi","token":"unchanged"}`)
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), identity, 0600); err != nil {
		t.Fatal(err)
	}
	if readGPSPolicy(dir, "pi") {
		t.Fatal("GPS enabled without explicit opt-in")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PitPilot-Capabilities") != "gps-v1" {
			t.Error("configuration not negotiated")
		}
		json.NewEncoder(w).Encode(DeviceConfig{DeviceID: "pi", VehicleID: "v", ProtocolVersion: 1, GPSRecording: true})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", true)
	if err != nil {
		t.Fatal(err)
	}
	state := &runtimeState{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { refreshGPSPolicy(ctx, dir, "pi", client, state); close(done) }()
	deadline := time.After(time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
loop:
	for {
		select {
		case <-tick.C:
			if readGPSPolicy(dir, "pi") {
				break loop
			}
		case <-deadline:
			t.Fatal("enabled policy did not persist")
		}
	}
	cancel()
	<-done
	if readGPSPolicy(dir, "another-device") {
		t.Fatal("policy crossed enrollment")
	}
	if info, err := os.Stat(filepath.Join(dir, "gps-policy.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("policy is not private")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "identity.json"))
	if string(after) != string(identity) {
		t.Fatal("policy changed rollback identity")
	}
}

func TestOldBackendRejectionRetainsGPSUntilAccepted(t *testing.T) {
	q, err := OpenQueue(t.TempDir(), "pi", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	batch := gpsBatch(testGPSFix(), "session")
	if err = q.AppendGPS(batch); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			w.WriteHeader(422)
			return
		}
		if n, _, _ := q.Status(); n != 1 {
			t.Error("rejected GPS lost before acknowledgement")
		}
		json.NewEncoder(w).Encode(map[string]any{"batchId": batch.BatchID, "contextsCreated": 1})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { upload(ctx, q, client, &runtimeState{}); close(done) }()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ticker.C:
			n, _, _ := q.Status()
			if n == 0 {
				break loop
			}
		case <-deadline:
			t.Fatal("GPS did not retry and acknowledge")
		}
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatal("no retained retry")
	}
}
