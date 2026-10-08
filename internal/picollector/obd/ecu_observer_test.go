package obdcollector

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/RyoheiHashimoto/obd2"
)

func TestECUObserverDiscoveryFailurePreservesEarlierReplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		reply   string
		want    int
	}{
		{"adapter_initialization", "ATTP2", "?", 0},
		{"no_engine", "010C", "NO DATA", 0},
		{"other_ecu", "010C", "486B18410C0000AA", 0},
		{"truncated_rpm", "010C", "486B10410C00AA", 0},
		{"wrong_pid", "010C", "486B10410D00AA", 0},
		{"negative_rpm", "010C", "486B107F0112AA", 1},
		{"wrong_negative_service", "010C", "486B107F0912AA", 0},
		{"unsupported_pids", "0100", "NO DATA", 1},
		{"supported_pid_failure", "0100", "BUS ERROR", 1},
		{"malformed_supported_pid", "0100", "486B10410000AA", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			elm.set(tc.command, tc.reply)
			if tc.command == "010C" {
				elm.set("0100", "NO DATA")
			}
			calls := 0
			device, err := openObservedStream(testContext(t), port, func() { calls++ })
			if err == nil || device != nil {
				t.Fatalf("expected failed discovery, got %v, %v", device, err)
			}
			if calls != tc.want {
				t.Fatalf("ECU replies = %d, want %d", calls, tc.want)
			}
		})
	}
}

func TestECUObserverSurvivesLaterSampleFailure(t *testing.T) {
	elm, port := newScriptedELM(t)
	calls := 0
	device, err := openObservedStream(testContext(t), port, func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.Close() })
	if calls != 2 {
		t.Fatalf("discovery replies = %d, want RPM and supported PIDs", calls)
	}
	calls = 0
	elm.set("010C", "486B10410C0000AA")
	elm.set("0105", "BUS ERROR")
	if _, err := device.SampleDetails(testContext(t)); err == nil {
		t.Fatal("sample unexpectedly succeeded")
	}
	if calls != 1 {
		t.Fatalf("zero-RPM reply was lost after later sample failure: %d callbacks", calls)
	}
}

func TestECUObserverDoesNotCountAdapterResponses(t *testing.T) {
	elm, port := newScriptedELM(t)
	calls := 0
	device, err := openObservedStream(testContext(t), port, func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.Close() })
	calls = 0
	elm.set("010C", "NO DATA")
	sample, err := device.SampleDetails(testContext(t))
	if !errors.Is(err, ErrECUUnavailable) || sample.Power.Voltage == nil {
		t.Fatalf("expected adapter voltage without ECU data: %#v, %v", sample, err)
	}
	if calls != 0 {
		t.Fatalf("adapter voltage was counted as an ECU reply: %d", calls)
	}
}

func TestECUObserverCountsRefusalsBeforeOptionalFiltering(t *testing.T) {
	_, port := newScriptedELM(t)
	calls := 0
	device, err := openObservedStream(testContext(t), port, func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.Close() })
	responses := []obd2.Response{{ECU: 0x10, Protocol: obd2.ProtocolJ1850VPW, Data: []byte{0x7F, 0x07, 0x12}}}
	device.client = obd2.NewClient(ecuTransport{ecu: 0x10, transport: observedECUTransport{
		transport: observerReply{responses: responses}, onResponse: func() { calls++ },
	}})
	calls = 0
	if _, err := device.client.PendingDTCs(testContext(t)); !errors.Is(err, obd2.ErrNoResponse) {
		t.Fatalf("optional refusal should still be filtered: %v", err)
	}
	if calls != 1 {
		t.Fatalf("valid ECU refusal was not observed: %d", calls)
	}
}

func TestECUObserverMatchesEngineReplyAndPreservesTransportResult(t *testing.T) {
	for _, tc := range []struct {
		name     string
		request  []byte
		data     []byte
		ecu      uint32
		protocol obd2.Protocol
		err      error
		want     int
	}{
		{name: "rpm_zero", request: []byte{1, 12}, data: []byte{0x41, 12, 0, 0}, want: 1},
		{name: "negative", request: []byte{1, 12}, data: []byte{0x7F, 1, 0x12}, want: 1},
		{name: "pending", request: []byte{1, 12}, data: []byte{0x7F, 1, 0x78}, want: 1},
		{name: "empty_dtc", request: []byte{3}, data: []byte{0x43, 0, 0}, want: 1},
		{name: "other_ecu", request: []byte{1, 12}, data: []byte{0x41, 12, 0, 0}, ecu: 0x18},
		{name: "other_protocol", request: []byte{1, 12}, data: []byte{0x41, 12, 0, 0}, protocol: obd2.ProtocolCAN11},
		{name: "wrong_service", request: []byte{1, 12}, data: []byte{0x42, 12, 0, 0}},
		{name: "wrong_pid", request: []byte{1, 12}, data: []byte{0x41, 13, 0, 0}},
		{name: "wrong_negative_service", request: []byte{1, 12}, data: []byte{0x7F, 9, 0x12}},
		{name: "short_negative", request: []byte{1, 12}, data: []byte{0x7F, 1}},
		{name: "long_negative", request: []byte{1, 12}, data: []byte{0x7F, 1, 0x12, 0}},
		{name: "invalid_negative", request: []byte{1, 12}, data: []byte{0x7F, 1, 0}},
		{name: "short_positive", request: []byte{1, 12}, data: []byte{0x41, 12, 0}},
		{name: "short_dtc", request: []byte{3}, data: []byte{0x43}},
		{name: "partial_dtc", request: []byte{3}, data: []byte{0x43, 0}},
		{name: "empty", request: []byte{1, 12}},
		{name: "no_request", data: []byte{0x41, 12, 0, 0}},
		{name: "incomplete_request", request: []byte{1}, data: []byte{0x7F, 1, 0x12}},
		{name: "transport_error", request: []byte{1, 12}, data: []byte{0x41, 12, 0, 0}, err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ecu == 0 {
				tc.ecu = 0x10
			}
			if tc.protocol == obd2.ProtocolUnknown {
				tc.protocol = obd2.ProtocolJ1850VPW
			}
			responses := []obd2.Response{{ECU: tc.ecu, Protocol: tc.protocol, Data: tc.data}}
			calls := 0
			transport := observedECUTransport{transport: observerReply{responses: responses, err: tc.err}, onResponse: func() { calls++ }}
			got, err := transport.RoundTrip(t.Context(), tc.request)
			if calls != tc.want || !reflect.DeepEqual(got, responses) || err != tc.err {
				t.Fatalf("callbacks %d want %d; responses %#v, error %v", calls, tc.want, got, err)
			}
			if transport.CombinesPIDs() {
				t.Fatal("VPW requests must not combine PIDs")
			}
		})
	}
}

type observerReply struct {
	responses []obd2.Response
	err       error
}

func (r observerReply) RoundTrip(context.Context, []byte) ([]obd2.Response, error) {
	return r.responses, r.err
}
