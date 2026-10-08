package obdcollector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/RyoheiHashimoto/obd2"
	"github.com/RyoheiHashimoto/obd2/elm327"
)

var extraNumericSignals = map[obd2.PID]string{
	obd2.ShortTermFuelTrimBank2:      "short_fuel_trim_bank2_pct",
	obd2.LongTermFuelTrimBank2:       "long_fuel_trim_bank2_pct",
	obd2.FuelRailPressure:            "fuel_rail_pressure_kpa",
	obd2.FuelRailGaugePressure:       "fuel_rail_gauge_kpa",
	obd2.CommandedEGR:                "egr_commanded_pct",
	obd2.EGRError:                    "egr_error_pct",
	obd2.CommandedEvapPurge:          "evap_purge_pct",
	obd2.EvapVaporPressure:           "evap_pressure_pa",
	obd2.BarometricPressure:          "barometric_kpa",
	obd2.CatalystTempBank1Sensor1:    "catalyst_b1s1_c",
	obd2.CatalystTempBank2Sensor1:    "catalyst_b2s1_c",
	obd2.CatalystTempBank1Sensor2:    "catalyst_b1s2_c",
	obd2.CatalystTempBank2Sensor2:    "catalyst_b2s2_c",
	obd2.AbsoluteLoad:                "absolute_load_pct",
	obd2.CommandedEquivalenceRatio:   "commanded_equivalence_ratio",
	obd2.RelativeThrottlePosition:    "relative_throttle_pct",
	obd2.AmbientAirTemp:              "ambient_c",
	obd2.AbsoluteThrottlePositionB:   "throttle_b_pct",
	obd2.AbsoluteThrottlePositionC:   "throttle_c_pct",
	obd2.AcceleratorPedalPositionD:   "pedal_d_pct",
	obd2.AcceleratorPedalPositionE:   "pedal_e_pct",
	obd2.AcceleratorPedalPositionF:   "pedal_f_pct",
	obd2.CommandedThrottleActuator:   "commanded_throttle_pct",
	obd2.TimeRunWithMIL:              "time_with_mil_min",
	obd2.TimeSinceCodesCleared:       "time_since_clear_min",
	obd2.EthanolFuelPercent:          "ethanol_pct",
	obd2.AbsoluteEvapVaporPressure:   "evap_absolute_kpa",
	obd2.EvapVaporPressureWide:       "evap_pressure_wide_pa",
	obd2.FuelRailAbsolutePressure:    "fuel_rail_absolute_kpa",
	obd2.RelativeAcceleratorPosition: "relative_pedal_pct",
	obd2.EngineOilTemp:               "oil_c",
	obd2.FuelInjectionTiming:         "fuel_injection_deg",
	obd2.EngineFuelRate:              "fuel_rate_lph",
}

func extraSignalNames(pid obd2.PID) []string {
	if name, ok := extraNumericSignals[pid]; ok {
		return []string{name}
	}
	if pid == obd2.FuelSystemStatus {
		return []string{"fuel_system_1_status", "fuel_system_2_status"}
	}
	if pid >= obd2.O2Sensor1 && pid < obd2.O2Sensor1+8 {
		sensor := pid - obd2.O2Sensor1 + 1
		return []string{fmt.Sprintf("o2_sensor_%d_v", byte(sensor)), fmt.Sprintf("o2_sensor_%d_trim_pct", byte(sensor))}
	}
	return nil
}

func decodeExtra(reading obd2.Reading, values map[string]float64) error {
	info, ok := reading.PID.Info()
	if !ok || len(reading.Data) < info.Size {
		return errors.New("truncated additional engine PID")
	}
	if name, ok := extraNumericSignals[reading.PID]; ok {
		value, err := reading.Float()
		if err != nil {
			return err
		}
		if validReading(name, value) {
			values[name] = value
		}
		return nil
	}
	names := extraSignalNames(reading.PID)
	if reading.PID == obd2.FuelSystemStatus {
		for _, status := range reading.Data[:2] {
			switch status {
			case 0, 1, 2, 4, 8, 16:
			default:
				return errors.New("invalid fuel system status")
			}
		}
		for i, status := range reading.Data[:2] {
			if status != 0 {
				values[names[i]] = float64(status)
			}
		}
		return nil
	}
	if len(names) == 2 {
		values[names[0]] = float64(reading.Data[0]) / 200
		// FF marks a sensor that does not participate in fuel-trim calculation.
		if reading.Data[1] != 0xFF {
			values[names[1]] = (float64(reading.Data[1]) - 128) * 100 / 128
		}
	}
	return nil
}

func decodeMonitor(reading obd2.Reading, values map[string]float64) error {
	if len(reading.Data) < 4 {
		return errors.New("truncated engine monitor status")
	}
	data := reading.Data
	values["mil_on"] = float64(data[0] >> 7)
	values["dtc_count"] = float64(data[0] & 0x7F)
	values["ignition_compression"] = float64((data[1] >> 3) & 1)
	values["readiness_supported_count"] = 0
	values["readiness_incomplete_count"] = 0
	add := func(name string, supported, incomplete bool) {
		if !supported || name == "" {
			return
		}
		values["readiness_supported_count"]++
		values["readiness_"+name+"_ready"] = 1
		if incomplete {
			values["readiness_"+name+"_ready"] = 0
			values["readiness_incomplete_count"]++
		}
	}
	for bit, name := range []string{"misfire", "fuel_system", "components"} {
		add(name, data[1]&(1<<bit) != 0, data[1]&(1<<(bit+4)) != 0)
	}
	monitors := []string{"catalyst", "heated_catalyst", "evap", "secondary_air", "ac_refrigerant", "oxygen_sensor", "oxygen_heater", "egr"}
	if data[1]&0x08 != 0 {
		monitors = []string{"nmhc_catalyst", "nox_scr", "", "boost", "", "exhaust_sensor", "particulate_filter", "egr"}
	}
	for bit, name := range monitors {
		add(name, data[2]&(1<<bit) != 0, data[3]&(1<<bit) != 0)
	}
	return nil
}

func optionalUnavailable(err error) bool {
	if errors.Is(err, obd2.ErrNoResponse) {
		return true
	}
	var refusal *obd2.NegativeResponseError
	return errors.As(err, &refusal) && (refusal.Code == 0x11 || refusal.Code == 0x12 || refusal.Code == 0x31)
}

func (d *Device) readAdapterCounters(ctx context.Context, values map[string]float64) (int, error) {
	omitted := 0
	for _, counter := range []struct{ command, signal string }{
		{"STDICES", "adapter_engine_start_count"},
		{"STDICPO", "adapter_power_on_count"},
		{"STDITPO", "adapter_uptime_s"},
	} {
		lines, err := d.adapter.Command(ctx, counter.command)
		if err != nil {
			return omitted, sampleError(ctx, "read_counter", err)
		}
		for _, line := range lines {
			if adapterFault(line) {
				return omitted, sampleError(ctx, "read_counter", elm327.ErrAdapter)
			}
		}
		if len(lines) == 0 || (len(lines) == 1 && (lines[0] == "?" || lines[0] == "NO DATA")) {
			continue
		}
		if len(lines) != 1 {
			omitted++
			continue
		}
		count, err := strconv.ParseUint(lines[0], 10, 32)
		if err != nil {
			omitted++
			continue
		}
		values[counter.signal] = float64(count)
	}
	return omitted, nil
}

func adapterFault(line string) bool {
	// Command returns raw text; only RoundTrip classifies the adapter's fault replies.
	return strings.HasPrefix(line, "ERR") ||
		((strings.HasPrefix(line, "SEARCHING") || strings.HasPrefix(line, "BUS INIT")) && strings.HasSuffix(line, "ERROR")) ||
		slices.Contains([]string{"ACT ALERT", "BUFFER FULL", "BUS BUSY", "BUS ERROR", "CAN ERROR", "DATA ERROR", "FB ERROR", "LP ALERT", "LV RESET", "STOPPED", "UNABLE TO CONNECT"}, strings.TrimLeft(line, "!"))
}
