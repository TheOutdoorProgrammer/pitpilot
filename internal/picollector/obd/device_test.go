package obdcollector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
)

func TestVPWSampleKeepsEngineECUAndUsesLibraryDecoders(t *testing.T) {
	elm, port := newScriptedELM(t)
	d, err := openStream(testContext(t), port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	values, codes, err := d.Sample(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"rpm": 1726, "speed_kph": 50, "coolant_c": 90, "intake_c": 20,
		"short_fuel_trim_pct": 12.5, "long_fuel_trim_pct": -12.5,
		"manifold_kpa": 100, "maf_gps": 12.34, "run_time_s": 3600,
		"mil_on": 1, "dtc_count": 2, "adapter_voltage_v": 13.8,
		"ignition_compression": 0, "readiness_supported_count": 3, "readiness_incomplete_count": 0,
		"readiness_misfire_ready": 1, "readiness_fuel_system_ready": 1, "readiness_components_ready": 1,
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("readings = %#v, want %#v", values, want)
	}
	if !slices.Equal(codes, []string{"P0133", "P0300"}) {
		t.Fatalf("engine codes = %v", codes)
	}
	if slices.Contains(d.Supported(), "throttle_pct") {
		t.Fatal("transmission-only PID was advertised as an engine PID")
	}
	_, codes, err = d.Sample(testContext(t))
	if err != nil || codes != nil {
		t.Fatalf("throttled DTC poll = %v, %v", codes, err)
	}
	commands := elm.commands()
	if !slices.Contains(commands, "ATTP2") || slices.Contains(commands, "0111") {
		t.Fatalf("protocol or supported-PID selection wrong: %v", commands)
	}
	dtcPolls := 0
	for _, command := range commands {
		switch {
		case command == "03":
			dtcPolls++
		case slices.Contains([]string{"07", "0A", "STDICES", "STDICPO", "STDITPO"}, command):
		case strings.HasPrefix(command, "01") && len(command) == 4:
		case slices.Contains([]string{"ATZ", "ATE0", "ATL0", "ATS0", "ATH1", "ATTP2", "ATDPN", "ATRV"}, command):
		default:
			t.Fatalf("unexpected command outside read-only allowlist: %q", command)
		}
	}
	if dtcPolls != 1 {
		t.Fatalf("DTCs polled %d times", dtcPolls)
	}
}

func TestDTCUnknownAndConfirmedEmptyRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		known bool
	}{
		{"no_data", "NO DATA", false},
		{"other_ecu_only", "486B1843070000000000AA", false},
		{"empty", "486B1043000000000000AA", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			elm.set("03", tc.reply)
			d, err := openStream(testContext(t), port)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			_, codes, err := d.Sample(testContext(t))
			if err != nil || (codes != nil) != tc.known || len(codes) != 0 {
				t.Fatalf("DTCs = %#v, error %v, want known=%v", codes, err, tc.known)
			}
		})
	}
}

func TestAdapterFailuresAreNotSuccessfulSamples(t *testing.T) {
	for _, command := range []string{"010C", "03"} {
		t.Run(command, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			d, err := openStream(testContext(t), port)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			elm.set(command, "BUS ERROR")
			_, codes, err := d.Sample(testContext(t))
			if err == nil || codes != nil {
				t.Fatalf("adapter failure produced codes=%#v, error=%v", codes, err)
			}
		})
	}
}

func TestMissingNumericPIDIsOmitted(t *testing.T) {
	elm, port := newScriptedELM(t)
	d, err := openStream(testContext(t), port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	elm.set("0105", "NO DATA")
	values, _, err := d.Sample(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := values["coolant_c"]; exists || values["rpm"] != 1726 {
		t.Fatalf("missing coolant affected other readings: %v", values)
	}
}

func TestTruncatedDTCResponseNeverClearsFaults(t *testing.T) {
	for _, reply := range []string{"486B1043AA", "486B104301AA", "486B1043013300AA"} {
		t.Run(reply, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			elm.set("03", reply)
			d, err := openStream(testContext(t), port)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			values, codes, err := d.Sample(testContext(t))
			if err != nil || codes != nil || values["rpm"] != 1726 {
				t.Fatalf("truncated DTC reply produced codes=%#v, error=%v", codes, err)
			}
		})
	}
}

func TestMissingEngineFailsAndClosesPort(t *testing.T) {
	for _, reply := range []string{"486B18410C1AF8AA", "486B18410C1AF8AA\r486B20410C0FA0AA"} {
		t.Run(reply, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			elm.set("010C", reply)
			tracked := &trackedStream{Conn: port}
			d, err := openStream(testContext(t), tracked)
			if err == nil || d != nil || tracked.closed != 1 {
				t.Fatalf("missing engine opened: device=%v error=%v closes=%d", d, err, tracked.closed)
			}
		})
	}
}

func TestInitializationFailureClosesPort(t *testing.T) {
	elm, port := newScriptedELM(t)
	elm.set("ATTP2", "?")
	tracked := &trackedStream{Conn: port}
	d, err := openStream(testContext(t), tracked)
	if err == nil || d != nil || tracked.closed != 1 {
		t.Fatalf("failed adapter opened: device=%v error=%v closes=%d", d, err, tracked.closed)
	}
}

func TestFailuresIdentifyStageWithoutAdapterResponse(t *testing.T) {
	for _, tc := range []struct {
		command, reply, category string
		sample                   bool
	}{
		{"ATTP2", "private-adapter-reply", "obd.initialize_adapter.adapter_error", false},
		{"010C", "NO DATA", "obd.identify_engine.no_response", false},
		{"010C", "486B18410C1AF8AA", "obd.identify_engine.missing_engine", false},
		{"010C", "BUS ERROR", "obd.read_rpm.adapter_error", true},
		{"0105", "BUS ERROR", "obd.read_pids.adapter_error", true},
		{"03", "BUS ERROR", "obd.read_dtcs.adapter_error", true},
	} {
		t.Run(tc.category, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			var err error
			if tc.sample {
				d, openErr := openStream(testContext(t), port)
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer d.Close()
				elm.set(tc.command, tc.reply)
				_, err = d.SampleDetails(testContext(t))
			} else {
				elm.set(tc.command, tc.reply)
				_, err = openStream(testContext(t), port)
			}
			if diag.ErrorType(err) != tc.category {
				t.Fatalf("failure = %v, category = %s", err, diag.ErrorType(err))
			}
			if strings.Contains(err.Error(), tc.reply) {
				t.Fatal("failure text exposed adapter reply")
			}
		})
	}
}

func TestLostSerialConnectionRequiresReconnect(t *testing.T) {
	_, port := newScriptedELM(t)
	d, err := openStream(testContext(t), port)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	_, codes, err := d.Sample(testContext(t))
	if err == nil || codes != nil {
		t.Fatalf("disconnected sample = %#v, %v", codes, err)
	}
}

func TestOpenRejectsCanceledContextBeforePortAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Open(ctx, "/not/a/serial/port", 115200)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestCancellationInterruptsBlockedWrites(t *testing.T) {
	for _, stage := range []string{"initialize", "sample"} {
		t.Run(stage, func(t *testing.T) {
			_, port := newScriptedELM(t)
			stream := &blockedWriteStream{ReadWriteCloser: port, entered: make(chan struct{}), closed: make(chan struct{})}
			t.Cleanup(func() { _ = stream.Close() })
			operation := func(ctx context.Context) error {
				_, err := openStream(ctx, stream)
				return err
			}
			if stage == "sample" {
				d, err := openStream(testContext(t), stream)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.Close() })
				operation = func(ctx context.Context) error { _, _, err := d.Sample(ctx); return err }
			}
			stream.block.Store(true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- operation(ctx) }()
			select {
			case <-stream.entered:
			case <-time.After(time.Second):
				t.Fatal("operation did not start its write")
			}
			cancel()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("canceled write succeeded")
				}
				if !errors.Is(err, context.Canceled) || !strings.HasSuffix(diag.ErrorType(err), ".canceled") {
					t.Fatalf("cancellation was masked by port closure: %v", err)
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("cancellation did not interrupt the blocked write")
			}
		})
	}
}

func TestCompletedOperationsDoNotKeepCancellationHooks(t *testing.T) {
	_, port := newScriptedELM(t)
	ctx, cancel := context.WithCancel(testContext(t))
	d, err := openStream(ctx, port)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx, cancel = context.WithCancel(testContext(t))
	_, _, err = d.Sample(ctx)
	cancel()
	if err != nil {
		t.Fatalf("canceling completed Open closed device: %v", err)
	}
	if _, _, err := d.Sample(testContext(t)); err != nil {
		t.Fatalf("canceling completed Sample closed device: %v", err)
	}
}

type blockedWriteStream struct {
	io.ReadWriteCloser
	block     atomic.Bool
	entered   chan struct{}
	closed    chan struct{}
	enterOnce sync.Once
	closeOnce sync.Once
}

func (s *blockedWriteStream) Write(p []byte) (int, error) {
	if s.block.Load() {
		s.enterOnce.Do(func() { close(s.entered) })
		<-s.closed
		return 0, io.ErrClosedPipe
	}
	return s.ReadWriteCloser.Write(p)
}

func (s *blockedWriteStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed); _ = s.ReadWriteCloser.Close() })
	return nil
}

type trackedStream struct {
	net.Conn
	closed int
}

func (s *trackedStream) Close() error {
	s.closed++
	return s.Conn.Close()
}

type scriptedELM struct {
	mu      sync.Mutex
	replies map[string]string
	sent    []string
}

func newScriptedELM(t *testing.T) (*scriptedELM, net.Conn) {
	t.Helper()
	var supported uint32
	for _, pid := range []uint{1, 5, 6, 7, 11, 12, 13, 15, 16, 31} {
		supported |= uint32(1) << (32 - pid)
	}
	elm := &scriptedELM{replies: map[string]string{
		"ATZ": "ELM327 v2.2", "ATE0": "ATE0\rOK", "ATL0": "OK", "ATS0": "OK",
		"ATH1": "OK", "ATTP2": "OK", "ATDPN": "2", "ATRV": "13.8V",
		"0100": fmt.Sprintf("486B104100%08XAA\r486B18410000108001AA", supported),
		"0101": "486B10410182070000AA\r486B18410181070000AA",
		"0105": "486B10410582AA", "0106": "486B10410690AA", "0107": "486B10410770AA",
		"010B": "486B10410B64AA", "010C": "486B10410C1AF8AA\r486B18410C0FA0AA",
		"010D": "486B10410D32AA", "010F": "486B10410F3CAA", "0110": "486B10411004D2AA",
		"011F": "486B10411F0E10AA",
		"03":   "486B1043013300000000AA\r486B1843070000000000AA\r486B1043030000000000AA",
		"07":   "NO DATA", "0A": "NO DATA",
	}}
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		for {
			command, err := reader.ReadString('\r')
			if err != nil {
				return
			}
			command = strings.TrimSuffix(command, "\r")
			elm.mu.Lock()
			elm.sent = append(elm.sent, command)
			reply, exists := elm.replies[command]
			elm.mu.Unlock()
			if !exists {
				reply = "?"
			}
			if _, err := io.WriteString(server, reply+"\r>"); err != nil {
				return
			}
		}
	}()
	return elm, client
}

func (e *scriptedELM) set(command, reply string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.replies[command] = reply
}

func (e *scriptedELM) commands() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sent)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
