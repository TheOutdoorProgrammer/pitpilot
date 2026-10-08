package obdcollector

import (
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RyoheiHashimoto/obd2"
	"github.com/RyoheiHashimoto/obd2/elm327"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	"go.bug.st/serial"
)

var numericSignals = map[obd2.PID]string{
	obd2.EngineRPM:                 "rpm",
	obd2.VehicleSpeed:              "speed_kph",
	obd2.CoolantTemp:               "coolant_c",
	obd2.IntakeAirTemp:             "intake_c",
	obd2.EngineLoad:                "load_pct",
	obd2.ThrottlePosition:          "throttle_pct",
	obd2.MAFAirFlowRate:            "maf_gps",
	obd2.IntakeManifoldPressure:    "manifold_kpa",
	obd2.FuelTankLevel:             "fuel_level_pct",
	obd2.ShortTermFuelTrimBank1:    "short_fuel_trim_pct",
	obd2.LongTermFuelTrimBank1:     "long_fuel_trim_pct",
	obd2.TimingAdvance:             "timing_advance_deg",
	obd2.RunTimeSinceStart:         "run_time_s",
	obd2.ControlModuleVoltage:      "control_voltage_v",
	obd2.DistanceSinceCodesCleared: "distance_since_clear_km",
	obd2.WarmUpsSinceCodesCleared:  "warmups_since_clear",
	obd2.DistanceWithMIL:           "distance_with_mil_km",
	obd2.FuelPressure:              "fuel_pressure_kpa",
}

type Device struct {
	adapter   *elm327.Adapter
	transport obd2.Transport
	client    *obd2.Client
	pids      []obd2.PID
	extraPIDs []obd2.PID
	nextExtra int
	monitor   bool
	mu        sync.Mutex
	nextDTC   time.Time
	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, portPath string, baud int) (*Device, error) {
	return OpenWithECUObserver(ctx, portPath, baud, nil)
}

// OpenWithECUObserver reports engine replies before later decoding or discovery can fail.
// The observer runs synchronously and must return promptly without calling back into Device.
func OpenWithECUObserver(ctx context.Context, portPath string, baud int, onResponse func()) (*Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if portPath == "" || baud <= 0 {
		return nil, errors.New("serial port and positive baud rate are required")
	}
	port, err := serial.Open(portPath, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, sampleError(ctx, "open_serial", err)
	}
	return openObservedStream(ctx, port, onResponse)
}

func openStream(ctx context.Context, port io.ReadWriteCloser) (*Device, error) {
	return openObservedStream(ctx, port, nil)
}

func openObservedStream(ctx context.Context, port io.ReadWriteCloser, onResponse func()) (*Device, error) {
	if err := ctx.Err(); err != nil {
		_ = port.Close()
		return nil, err
	}
	stop := closeOnCancel(ctx, port)
	defer stop()
	adapter, err := elm327.Open(ctx, port, elm327.Options{Protocol: elm327.ProtocolJ1850VPW})
	if err != nil {
		_ = port.Close()
		return nil, sampleError(ctx, "initialize_adapter", err)
	}
	d := &Device{adapter: adapter, transport: observedECUTransport{transport: adapter, onResponse: onResponse}}
	if err := d.discover(ctx); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Device) discover(ctx context.Context) error {
	discovery := obd2.NewClient(d.transport)
	readings, err := discovery.Query(ctx, obd2.EngineRPM)
	if err != nil {
		return sampleError(ctx, "identify_engine", err)
	}
	if discovery.Protocol() != obd2.ProtocolJ1850VPW {
		return diag.NewError("obd.identify_engine.wrong_protocol", nil)
	}
	// Legacy source 0x10 identifies the engine; a lone transmission response is not a substitute.
	const engine = 0x10
	if !slices.ContainsFunc(readings, func(reading obd2.Reading) bool { return reading.ECU == engine }) {
		return diag.NewError("obd.identify_engine.missing_engine", nil)
	}
	d.client = obd2.NewClient(ecuTransport{transport: d.transport, ecu: engine})
	supported, err := d.client.SupportedPIDs(ctx)
	if err != nil {
		return sampleError(ctx, "discover_pids", err)
	}
	if !slices.Contains(supported, obd2.EngineRPM) {
		return diag.NewError("obd.discover_pids.rpm_unsupported", nil)
	}
	for _, pid := range supported {
		if pid == obd2.EngineRPM {
			continue
		}
		if _, ok := numericSignals[pid]; ok {
			d.pids = append(d.pids, pid)
		} else if len(extraSignalNames(pid)) > 0 {
			d.extraPIDs = append(d.extraPIDs, pid)
		}
	}
	d.monitor = slices.Contains(supported, obd2.MonitorStatus)
	return nil
}

func (d *Device) Supported() []string {
	names := []string{"adapter_voltage_v", "rpm"}
	for _, pid := range d.pids {
		names = append(names, numericSignals[pid])
	}
	for _, pid := range d.extraPIDs {
		names = append(names, extraSignalNames(pid)...)
	}
	if d.monitor {
		names = append(names, "mil_on", "dtc_count", "ignition_compression", "readiness_supported_count", "readiness_incomplete_count")
	}
	slices.Sort(names)
	return names
}

func (d *Device) Close() error {
	d.closeOnce.Do(func() { d.closeErr = d.adapter.Close() })
	return d.closeErr
}

type Observation struct {
	Readings      map[string]float64
	DTCs          []string
	PendingDTCs   []string
	PermanentDTCs []string
	Power         PowerObservation
}

type PowerObservation struct {
	At        time.Time
	Voltage   *float64
	RPM       *float64
	ECUNoData bool
}

var ErrECUUnavailable = errors.New("engine ECU returned no data; adapter voltage only")

func (d *Device) SamplePower(ctx context.Context) (PowerObservation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PowerObservation{}, err
	}
	stop := closeOnCancel(ctx, d)
	defer stop()
	return d.readPower(ctx)
}

func (d *Device) readPower(ctx context.Context) (PowerObservation, error) {
	power := PowerObservation{At: time.Now()}
	readings, err := d.client.Query(ctx, obd2.EngineRPM)
	if errors.Is(err, obd2.ErrNoResponse) {
		// Upstream also uses ErrNoResponse for empty or filtered replies. Only literal NO DATA supports this inference.
		lines, confirmationErr := d.adapter.Command(ctx, "010C")
		if confirmationErr != nil {
			return PowerObservation{}, sampleError(ctx, "confirm_no_data", confirmationErr)
		}
		if len(lines) != 1 || lines[0] != "NO DATA" {
			return PowerObservation{}, sampleError(ctx, "read_rpm", err)
		}
		power.ECUNoData = true
	} else if err != nil {
		return PowerObservation{}, sampleError(ctx, "read_rpm", err)
	} else {
		if len(readings) != 1 {
			return PowerObservation{}, diag.NewError("obd.read_rpm.ambiguous_reply", nil)
		}
		rpm, err := readings[0].Float()
		if err != nil || math.IsNaN(rpm) || rpm < 0 || rpm > 20000 {
			return PowerObservation{}, diag.NewError("obd.read_rpm.invalid_reply", err)
		}
		power.RPM = &rpm
	}
	lines, err := d.adapter.Command(ctx, "ATRV")
	if err != nil {
		return PowerObservation{}, sampleError(ctx, "read_voltage", err)
	}
	if len(lines) == 1 && strings.HasSuffix(lines[0], "V") {
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(lines[0], "V")), 64)
		if err == nil && v >= 0 && v <= 40 {
			power.Voltage = &v
		}
	}
	return power, nil
}

func (d *Device) Sample(ctx context.Context) (map[string]float64, []string, error) {
	sample, err := d.SampleDetails(ctx)
	return sample.Readings, sample.DTCs, err
}

func (d *Device) SampleDetails(ctx context.Context) (Observation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	stop := closeOnCancel(ctx, d)
	defer stop()
	optionalFailures := 0
	defer func() {
		if optionalFailures > 0 {
			_ = diag.Operation(ctx, "obd.diagnostics", func(context.Context) error { return errOptionalDiagnostics })
		}
	}()
	omitOptional := func(err error) bool {
		if errors.Is(err, obd2.ErrNoResponse) {
			return true
		}
		var refusal *obd2.NegativeResponseError
		if errors.As(err, &refusal) || errors.Is(err, errMalformedDTC) {
			optionalFailures++
			return true
		}
		return false
	}
	power, err := d.readPower(ctx)
	if err != nil {
		return Observation{}, err
	}
	values := make(map[string]float64, len(d.pids)+3)
	if power.Voltage != nil {
		values["adapter_voltage_v"] = *power.Voltage
	}
	if power.ECUNoData {
		return Observation{Readings: values, Power: power}, ErrECUUnavailable
	}
	values["rpm"] = *power.RPM
	readings, err := d.client.Query(ctx, d.pids...)
	if err != nil && !errors.Is(err, obd2.ErrNoResponse) {
		return Observation{}, sampleError(ctx, "read_pids", err)
	}
	for _, reading := range readings {
		value, err := reading.Float()
		if err != nil {
			return Observation{}, sampleError(ctx, "decode_pid", err)
		}
		name := numericSignals[reading.PID]
		if validReading(name, value) {
			values[name] = value
		}
	}
	if d.monitor {
		status, err := d.client.Query(ctx, obd2.MonitorStatus)
		if err != nil && !omitOptional(err) {
			return Observation{}, sampleError(ctx, "read_monitor", err)
		}
		for _, reading := range status {
			if err := decodeMonitor(reading, values); err != nil {
				optionalFailures++
			}
		}
	}
	// VPW permits one PID per request, so rotate extra sensors instead of growing every poll without bound.
	for range min(8, len(d.extraPIDs)) {
		pid := d.extraPIDs[d.nextExtra]
		d.nextExtra = (d.nextExtra + 1) % len(d.extraPIDs)
		readings, err := d.client.Query(ctx, pid)
		if omitOptional(err) {
			continue
		}
		if err != nil {
			return Observation{}, sampleError(ctx, "read_extra_pid", err)
		}
		for _, reading := range readings {
			if err := decodeExtra(reading, values); err != nil {
				optionalFailures++
			}
		}
	}
	sample := Observation{Readings: values, Power: power}
	if time.Now().Before(d.nextDTC) {
		return sample, nil
	}
	d.nextDTC = time.Now().Add(time.Minute)
	for _, diagnostic := range []struct {
		query func(context.Context) ([]obd2.DTC, error)
		codes *[]string
	}{
		{d.client.StoredDTCs, &sample.DTCs},
		{d.client.PendingDTCs, &sample.PendingDTCs},
		{d.client.PermanentDTCs, &sample.PermanentDTCs},
	} {
		codes, err := diagnostic.query(ctx)
		if omitOptional(err) {
			continue
		}
		if err != nil {
			return Observation{}, sampleError(ctx, "read_dtcs", err)
		}
		*diagnostic.codes = make([]string, 0, len(codes))
		for _, code := range codes {
			*diagnostic.codes = append(*diagnostic.codes, code.String())
		}
	}
	omitted, err := d.readAdapterCounters(ctx, values)
	optionalFailures += omitted
	if err != nil {
		return Observation{}, err
	}
	return sample, nil
}

func closeOnCancel(ctx context.Context, port io.Closer) func() {
	done := make(chan struct{})
	// Upstream checks cancellation while reading but serial writes can block too.
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = port.Close()
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

type observedECUTransport struct {
	transport  obd2.Transport
	onResponse func()
}

func (t observedECUTransport) CombinesPIDs() bool { return false }

func (t observedECUTransport) RoundTrip(ctx context.Context, request []byte) ([]obd2.Response, error) {
	responses, err := t.transport.RoundTrip(ctx, request)
	if err == nil && t.onResponse != nil && slices.ContainsFunc(responses, func(response obd2.Response) bool {
		return answersEngineRequest(request, response)
	}) {
		t.onResponse()
	}
	return responses, err
}

func answersEngineRequest(request []byte, response obd2.Response) bool {
	if len(request) == 0 || response.ECU != 0x10 || response.Protocol != obd2.ProtocolJ1850VPW {
		return false
	}
	switch request[0] {
	case 0x01:
		if len(request) != 2 {
			return false
		}
	case 0x03, 0x07, 0x0A:
		if len(request) != 1 {
			return false
		}
	default:
		return false
	}
	data := response.Data
	if len(data) == 3 && data[0] == 0x7F {
		return data[1] == request[0] && data[2] != 0
	}
	if len(data) == 0 || len(data) > 7 || data[0] != request[0]+0x40 {
		return false
	}
	switch request[0] {
	case 0x01:
		if len(data) < 3 || data[1] != request[1] {
			return false
		}
		info, known := obd2.PID(request[1]).Info()
		return known && len(data) >= 2+info.Size
	case 0x03, 0x07, 0x0A:
		return len(data) >= 3 && len(data)%2 == 1
	default:
		return false
	}
}

type ecuTransport struct {
	transport obd2.Transport
	ecu       uint32
}

var errMalformedDTC = errors.New("malformed legacy DTC response")
var errOptionalDiagnostics = diag.NewError("obd.optional_diagnostics.omitted", nil)

func (t ecuTransport) CombinesPIDs() bool { return false }

func (t ecuTransport) RoundTrip(ctx context.Context, request []byte) ([]obd2.Response, error) {
	responses, err := t.transport.RoundTrip(ctx, request)
	if err != nil {
		return nil, err
	}
	responses = slices.DeleteFunc(responses, func(response obd2.Response) bool {
		return response.ECU != t.ecu || optionalUnavailable(response.Err())
	})
	if len(responses) == 0 {
		return nil, obd2.ErrNoResponse
	}
	if len(request) == 1 && slices.Contains([]byte{0x03, 0x07, 0x0A}, request[0]) {
		for _, response := range responses {
			if len(response.Data) > 0 && response.Data[0] == request[0]+0x40 {
				// Upstream accepts truncated legacy DTC payloads as empty; those must never clear a fault.
				if n := len(response.Data); n < 3 || n > 7 || n%2 != 1 {
					return nil, errMalformedDTC
				}
			}
		}
	}
	return responses, nil
}
