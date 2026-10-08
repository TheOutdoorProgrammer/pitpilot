// Package summarymetrics recognizes complete collector-generated notes without
// treating arbitrary user prose as disposable telemetry.
package summarymetrics

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

var ErrUnsupported = errors.New("note is not a supported, complete generated summary")

type parser struct {
	batch      garage.SignalBatch
	start, end time.Time
	lines      []string
	index      int
}

// Parse returns a batch only when every line and the generated record metadata
// are understood. Callers must retain the original when ErrUnsupported is returned.
func Parse(record garage.Record) (garage.SignalBatch, error) {
	if record.Kind != "note" || record.Source == nil || record.Source.System != "lubelogger" || record.Source.Collection != "notes" || record.ID == "" || record.Pinned || len(record.ExtraFields) != 0 || record.CostCents != 0 || record.Gallons != nil || record.Plan != nil || record.Fuel != nil || record.OdometerMiles != 0 || record.InitialOdometerMiles != nil || record.OdometerStatus != "" || record.Date != "" {
		return garage.SignalBatch{}, ErrUnsupported
	}
	hash := sha256.Sum256([]byte(record.ID))
	p := parser{batch: garage.SignalBatch{Source: "lubelogger", BatchID: fmt.Sprintf("summary-%x", hash[:16])}, lines: strings.Split(strings.ReplaceAll(record.Notes, "\r\n", "\n"), "\n")}
	var err error
	switch {
	case strings.HasPrefix(record.Notes, "OBD driving summary for "):
		err = p.obd(record)
	case strings.HasPrefix(record.Notes, "## Vehicle Status "):
		err = p.smartcar(record)
	default:
		err = ErrUnsupported
	}
	if err != nil || p.index != len(p.lines) || p.batch.Validate() != nil {
		return garage.SignalBatch{}, ErrUnsupported
	}
	return p.batch, nil
}

func (p *parser) day(day string) error {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil || t.Year() < 2020 || t.Year() > 2100 {
		return ErrUnsupported
	}
	p.start, p.end = t, t.AddDate(0, 0, 1)
	return nil
}

func (p *parser) line() string {
	if p.index >= len(p.lines) {
		return ""
	}
	l := p.lines[p.index]
	p.index++
	return l
}

func (p *parser) exact(want string) bool { return p.line() == want }

func match(pattern, line string) []string {
	return regexp.MustCompile("^(?:" + pattern + ")$").FindStringSubmatch(line)
}

func number(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return math.NaN()
	}
	return v
}
func integer(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return v
}

func (p *parser) observation(metric, unit, statistic, quality string, value float64, observed *time.Time) {
	o := garage.SignalObservation{Key: fmt.Sprintf("%s-%04d", p.batch.BatchID, len(p.batch.Observations)), Metric: metric, Unit: unit, Statistic: statistic, Quality: quality, Value: value}
	if observed != nil {
		o.ObservedAt = observed
	} else {
		o.PeriodStart, o.PeriodEnd = &p.start, &p.end
	}
	p.batch.Observations = append(p.batch.Observations, o)
}

func validTags(got []string, want ...string) bool {
	a, b := slices.Clone(got), slices.Clone(want)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
func (p *parser) stamp(s string) (*time.Time, error) {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil || t.Before(p.start) || !t.Before(p.end) {
		return nil, ErrUnsupported
	}
	return &t, nil
}

const numeric = `(-?[0-9]+(?:\.[0-9]+)?)`

type reading struct{ metric, unit, textUnit string }

var readings = map[string]reading{
	"Adapter engine start count":    {"adapter_engine_start_count", "count", ""},
	"Adapter power-on count":        {"adapter_power_on_count", "count", ""},
	"Adapter supply voltage":        {"adapter_voltage_v", "V", "V"},
	"Adapter uptime":                {"adapter_uptime_s", "s", "s"},
	"Check engine light":            {"mil_on", "boolean", ""},
	"Compression ignition":          {"ignition_compression", "boolean", ""},
	"Coolant temperature":           {"coolant_c", "°C", "°C"},
	"Engine RPM":                    {"rpm", "rpm", "rpm"},
	"Engine load":                   {"load_pct", "%", "%"},
	"Fuel system 1 status":          {"fuel_system_1_status", "code", ""},
	"Fuel system 2 status":          {"fuel_system_2_status", "code", ""},
	"Incomplete readiness monitors": {"readiness_incomplete_count", "count", ""},
	"Intake manifold pressure":      {"manifold_kpa", "kPa", "kPa"},
	"Intake temperature":            {"intake_c", "°C", "°C"},
	"Long term fuel trim":           {"long_fuel_trim_pct", "%", "%"},
	"Short term fuel trim":          {"short_fuel_trim_pct", "%", "%"},
	"Supported readiness monitors":  {"readiness_supported_count", "count", ""},
	"Throttle position":             {"throttle_pct", "%", "%"},
	"Timing advance":                {"timing_advance_deg", "deg", "°"},
	"Trouble code count":            {"dtc_count", "count", ""},
	"Vehicle speed":                 {"speed_kph", "km/h", "km/h"},
}

func init() {
	for n := 1; n <= 8; n++ {
		readings[fmt.Sprintf("Oxygen sensor %d voltage", n)] = reading{fmt.Sprintf("o2_sensor_%d_v", n), "V", "V"}
		readings[fmt.Sprintf("Oxygen sensor %d fuel trim", n)] = reading{fmt.Sprintf("o2_sensor_%d_trim_pct", n), "%", "%"}
	}
	for _, m := range []string{"misfire", "fuel_system", "components", "catalyst", "heated_catalyst", "evap", "secondary_air", "ac_refrigerant", "oxygen_sensor", "oxygen_heater", "egr", "nmhc_catalyst", "nox_scr", "boost", "exhaust_sensor", "particulate_filter"} {
		readings["Readiness "+strings.ReplaceAll(m, "_", " ")] = reading{"readiness_" + m + "_ready", "boolean", ""}
	}
}
