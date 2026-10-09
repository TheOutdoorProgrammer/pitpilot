package garage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
)

type SignalDefinition struct {
	Metric            string            `json:"metric"`
	Label             string            `json:"label"`
	Unit              string            `json:"unit"`
	StaleAfterSeconds int               `json:"staleAfterSeconds"`
	Description       string            `json:"description,omitempty"`
	Interpretation    string            `json:"interpretation,omitempty"`
	ValueLabels       map[string]string `json:"valueLabels,omitempty"`
}

type SignalObservation struct {
	Key            string     `json:"key"`
	Metric         string     `json:"metric"`
	Unit           string     `json:"unit"`
	Statistic      string     `json:"statistic"`
	Quality        string     `json:"quality"`
	Value          float64    `json:"value"`
	SampleCount    *int       `json:"sampleCount,omitempty"`
	ObservedAt     *time.Time `json:"observedAt,omitempty"`
	PeriodStart    *time.Time `json:"periodStart,omitempty"`
	PeriodEnd      *time.Time `json:"periodEnd,omitempty"`
	SourceRevision *time.Time `json:"sourceRevision,omitempty"`
	CalendarDate   string     `json:"calendarDate,omitempty"`
	Timezone       string     `json:"timezone,omitempty"`
}

func (o *SignalObservation) UnmarshalJSON(raw []byte) error {
	if err := jsonutil.Validate(raw); err != nil {
		return errors.New("invalid signal JSON")
	}
	if err := signalJSONKeys(raw, reflect.TypeFor[SignalObservation]()); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	value, ok := fields["value"]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("signal value is required; unknown is represented by no observation")
	}
	type plain SignalObservation
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*o = SignalObservation(decoded)
	return nil
}

type SignalBatch struct {
	Source       string              `json:"source"`
	BatchID      string              `json:"batchId"`
	Observations []SignalObservation `json:"observations"`
	Contexts     []SignalContext     `json:"contexts,omitempty"`
}

func (b *SignalBatch) UnmarshalJSON(raw []byte) error {
	if err := jsonutil.Validate(raw); err != nil {
		return errors.New("invalid signal batch JSON")
	}
	if err := signalJSONKeys(raw, reflect.TypeFor[SignalBatch]()); err != nil {
		return err
	}
	type plain SignalBatch
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*b = SignalBatch(decoded)
	return nil
}

type SignalContext struct {
	Key          string            `json:"key"`
	Kind         string            `json:"kind"`
	ObservedAt   *time.Time        `json:"observedAt,omitempty"`
	PeriodStart  *time.Time        `json:"periodStart,omitempty"`
	PeriodEnd    *time.Time        `json:"periodEnd,omitempty"`
	Coverage     *SignalCoverage   `json:"coverage,omitempty"`
	Diagnostic   *SignalDiagnostic `json:"diagnostic,omitempty"`
	Segment      *SignalSegment    `json:"segment,omitempty"`
	Location     *SignalLocation   `json:"location,omitempty"`
	Snapshot     *SignalSnapshot   `json:"snapshot,omitempty"`
	CalendarDate string            `json:"calendarDate,omitempty"`
	Timezone     string            `json:"timezone,omitempty"`
}

func (c *SignalContext) UnmarshalJSON(raw []byte) error {
	if err := jsonutil.Validate(raw); err != nil {
		return errors.New("invalid signal context JSON")
	}
	if err := signalJSONKeys(raw, reflect.TypeFor[SignalContext]()); err != nil {
		return err
	}
	type plain SignalContext
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for detail, required := range map[string][]string{"location": {"latitude", "longitude"}, "coverage": {"retainedSamples", "skippedIntervals"}, "diagnostic": {"successfulReads", "unknown"}, "segment": {"samples", "speedCoverageSeconds", "runningSeconds", "idleSeconds"}} {
		data, ok := fields[detail]
		if !ok || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			continue
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		for _, key := range required {
			value, ok := values[key]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errors.New("context measurements and states must be explicit")
			}
		}
		if detail == "diagnostic" && decoded.Diagnostic != nil && !decoded.Diagnostic.Unknown && decoded.Diagnostic.Codes == nil {
			return errors.New("known diagnostic states require an explicit codes array")
		}
	}
	*c = SignalContext(decoded)
	return nil
}

// encoding/json accepts case-insensitive aliases; the wire contract does not.
// Exact tagged names keep required-field checks and decoding in agreement.
func signalJSONKeys(raw []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeFor[time.Time]() {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	allowed := make(map[string]reflect.Type, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		allowed[name] = field.Type
	}
	for name, value := range fields {
		fieldType, ok := allowed[name]
		if !ok {
			return errors.New("signal JSON requires canonical field names")
		}
		if err := signalJSONKeys(value, fieldType); err != nil {
			return err
		}
	}
	return nil
}

type SignalSnapshot struct {
	Date      string `json:"date"`
	Timezone  string `json:"timezone"`
	Precision string `json:"precision"`
}

type SignalCoverage struct {
	FirstObservedAt  *time.Time `json:"firstObservedAt,omitempty"`
	LastObservedAt   *time.Time `json:"lastObservedAt,omitempty"`
	RetainedSamples  int        `json:"retainedSamples"`
	SkippedIntervals int        `json:"skippedIntervals"`
}

type SignalDiagnostic struct {
	Class           string   `json:"class"`
	Codes           []string `json:"codes"`
	SuccessfulReads int      `json:"successfulReads"`
	Unknown         bool     `json:"unknown"`
}

type SignalSegment struct {
	StartedAt            time.Time `json:"startedAt"`
	EndedAt              time.Time `json:"endedAt"`
	State                string    `json:"state"`
	Samples              int       `json:"samples"`
	DistanceKM           *float64  `json:"distanceKm,omitempty"`
	SpeedCoverageSeconds float64   `json:"speedCoverageSeconds"`
	RunningSeconds       float64   `json:"runningSeconds"`
	IdleSeconds          float64   `json:"idleSeconds"`
	TopSpeedKPH          *float64  `json:"topSpeedKph,omitempty"`
}

type SignalLocation struct {
	Latitude       float64  `json:"latitude"`
	Longitude      float64  `json:"longitude"`
	Type           string   `json:"type"`
	AccuracyMeters *float64 `json:"accuracyMeters,omitempty"`
	RecordingID    string   `json:"recordingId,omitempty"`
	SpeedKPH       *float64 `json:"speedKph,omitempty"`
	CourseDegrees  *float64 `json:"courseDegrees,omitempty"`
	AltitudeMeters *float64 `json:"altitudeMeters,omitempty"`
	Satellites     *int     `json:"satellites,omitempty"`
	HDOP           *float64 `json:"hdop,omitempty"`
	FixQuality     *int     `json:"fixQuality,omitempty"`
}

type SignalIngestReport struct {
	Created         int `json:"created"`
	Updated         int `json:"updated"`
	Skipped         int `json:"skipped"`
	ContextsCreated int `json:"contextsCreated"`
	ContextsSkipped int `json:"contextsSkipped"`
}

var ErrSignalConflict = errors.New("signal identity or revision conflicts with stored data")

var diagnosticCodePattern = regexp.MustCompile(`^[PBCU][0-3][0-9A-F]{3}$`)

var signalDefinitions = func() map[string]SignalDefinition {
	groups := map[string][]string{
		"count":   {"adapter_engine_start_count", "adapter_power_on_count", "readiness_incomplete_count", "readiness_supported_count", "dtc_count", "retained_samples", "skipped_intervals"},
		"V":       {"adapter_voltage_v", "o2_sensor_1_v", "o2_sensor_2_v"},
		"s":       {"adapter_uptime_s", "speed_coverage_s", "moving_time_s", "running_time_s", "idle_time_s"},
		"boolean": {"mil_on", "ignition_compression", "readiness_catalyst_ready", "readiness_components_ready", "readiness_evap_ready", "readiness_fuel_system_ready", "readiness_misfire_ready", "readiness_oxygen_heater_ready", "readiness_oxygen_sensor_ready", "readiness_secondary_air_ready"},
		"°C":      {"coolant_c", "intake_c"}, "rpm": {"rpm"},
		"%":    {"load_pct", "long_fuel_trim_pct", "o2_sensor_1_trim_pct", "short_fuel_trim_pct", "throttle_pct", "fuel_level_pct", "oil_life_pct"},
		"code": {"fuel_system_1_status"},
		"kPa":  {"manifold_kpa", "tire_fl_kpa", "tire_fr_kpa", "tire_rl_kpa", "tire_rr_kpa"},
		"deg":  {"timing_advance_deg"}, "km/h": {"speed_kph"},
		"km": {"distance_km", "driving_distance_km", "odometer_km", "range_km"}, "L": {"fuel_remaining_l"},
	}
	definitions := map[string]SignalDefinition{}
	groups["km/h"] = append(groups["km/h"], "daily_top_speed_kph")
	groups["code"] = append(groups["code"], "fuel_system_2_status")
	groups["count"] = append(groups["count"], "warmups_since_clear")
	groups["s"] = append(groups["s"], "run_time_s")
	groups["min"] = []string{"time_with_mil_min", "time_since_clear_min"}
	groups["g/s"] = []string{"maf_gps"}
	groups["L/h"] = []string{"fuel_rate_lph"}
	groups["ratio"] = []string{"commanded_equivalence_ratio"}
	groups["deg"] = append(groups["deg"], "fuel_injection_deg")
	groups["Pa"] = []string{"evap_pressure_pa", "evap_pressure_wide_pa"}
	groups["km"] = append(groups["km"], "distance_since_clear_km", "distance_with_mil_km")
	groups["kPa"] = append(groups["kPa"], "fuel_pressure_kpa", "fuel_rail_pressure_kpa", "fuel_rail_gauge_kpa", "barometric_kpa", "evap_absolute_kpa", "fuel_rail_absolute_kpa")
	groups["°C"] = append(groups["°C"], "catalyst_b1s1_c", "catalyst_b2s1_c", "catalyst_b1s2_c", "catalyst_b2s2_c", "ambient_c", "oil_c")
	groups["V"] = append(groups["V"], "control_voltage_v")
	groups["%"] = append(groups["%"], "short_fuel_trim_bank2_pct", "long_fuel_trim_bank2_pct", "egr_commanded_pct", "egr_error_pct", "evap_purge_pct", "absolute_load_pct", "relative_throttle_pct", "throttle_b_pct", "throttle_c_pct", "pedal_d_pct", "pedal_e_pct", "pedal_f_pct", "commanded_throttle_pct", "ethanol_pct", "relative_pedal_pct")
	for n := 3; n <= 8; n++ {
		groups["V"] = append(groups["V"], fmt.Sprintf("o2_sensor_%d_v", n))
	}
	for n := 2; n <= 8; n++ {
		groups["%"] = append(groups["%"], fmt.Sprintf("o2_sensor_%d_trim_pct", n))
	}
	for _, name := range []string{"heated_catalyst", "ac_refrigerant", "egr", "nmhc_catalyst", "nox_scr", "boost", "exhaust_sensor", "particulate_filter"} {
		groups["boolean"] = append(groups["boolean"], "readiness_"+name+"_ready")
	}
	for unit, metrics := range groups {
		for _, metric := range metrics {
			definitions[metric] = SignalDefinition{Metric: metric, Label: strings.ReplaceAll(metric, "_", " "), Unit: unit, StaleAfterSeconds: 900}
		}
	}
	labels := map[string]string{"manifold_kpa": "Manifold pressure", "coolant_c": "Coolant temperature", "intake_c": "Intake temperature", "rpm": "Engine speed", "speed_kph": "Vehicle speed", "adapter_voltage_v": "Adapter voltage", "fuel_level_pct": "Fuel level", "oil_life_pct": "Oil life", "odometer_km": "Odometer", "range_km": "Estimated range", "distance_km": "Estimated distance", "driving_distance_km": "Captured distance"}
	for metric, label := range labels {
		d := definitions[metric]
		d.Label = label
		definitions[metric] = d
	}
	labels = map[string]string{"adapter_engine_start_count": "Adapter engine starts", "adapter_power_on_count": "Adapter power cycles", "adapter_uptime_s": "Adapter uptime", "mil_on": "Malfunction indicator", "ignition_compression": "Compression ignition", "load_pct": "Engine load", "readiness_incomplete_count": "Incomplete readiness monitors", "readiness_supported_count": "Supported readiness monitors", "long_fuel_trim_pct": "Long-term fuel trim", "short_fuel_trim_pct": "Short-term fuel trim", "throttle_pct": "Throttle position", "timing_advance_deg": "Timing advance", "dtc_count": "Diagnostic trouble code count", "retained_samples": "Retained samples", "skipped_intervals": "Skipped intervals", "speed_coverage_s": "Speed recording coverage", "moving_time_s": "Moving time", "running_time_s": "Engine running time", "idle_time_s": "Idle time", "fuel_remaining_l": "Fuel remaining", "fuel_system_1_status": "Fuel system 1 status", "fuel_system_2_status": "Fuel system 2 status", "tire_fl_kpa": "Front left tire pressure", "tire_fr_kpa": "Front right tire pressure", "tire_rl_kpa": "Rear left tire pressure", "tire_rr_kpa": "Rear right tire pressure"}
	for metric, d := range definitions {
		if label, ok := collectorSignalLabels[metric]; ok {
			d.Label = label
		}
		if metric == "daily_top_speed_kph" {
			d.Label = "Summary top speed"
		}
		if label, ok := labels[metric]; ok {
			d.Label = label
		}
		if strings.HasPrefix(metric, "readiness_") && strings.HasSuffix(metric, "_ready") {
			name := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(metric, "readiness_"), "_ready"), "_", " ")
			d.Label = strings.ToUpper(name[:1]) + name[1:] + " readiness"
		}
		for n := 1; n <= 8; n++ {
			if metric == fmt.Sprintf("o2_sensor_%d_v", n) {
				d.Label = fmt.Sprintf("Oxygen sensor %d voltage", n)
			}
			if metric == fmt.Sprintf("o2_sensor_%d_trim_pct", n) {
				d.Label = fmt.Sprintf("Oxygen sensor %d fuel trim", n)
			}
		}
		d.Description, d.Interpretation = explainSignal(metric)
		d.ValueLabels = signalValueLabels(metric)
		definitions[metric] = d
	}
	return definitions
}()

func SignalDefinitions() []SignalDefinition {
	result := make([]SignalDefinition, 0, len(signalDefinitions))
	for _, definition := range signalDefinitions {
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Metric < result[j].Metric })
	return result
}

func signalUnit(metric, statistic string) (string, bool) {
	d, ok := signalDefinitions[metric]
	if !ok {
		return "", false
	}
	if statistic == "count" {
		return "count", true
	}
	return d.Unit, true
}

func validSignalSource(source string) bool {
	return source == "pi" || source == "smartcar" || source == "lubelogger"
}
func validSignalKey(key string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 200 && !strings.ContainsAny(key, "\r\n\x00")
}
func signalTime(t *time.Time) bool {
	return t != nil && !t.IsZero() && t.Year() >= 1970 && t.Year() <= 9999
}
func finiteSignal(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 1e12
}

func signalWindow(observed, start, end *time.Time, date, zone string) error {
	if date != "" {
		if !validDate(date) || zone != "unknown" || observed != nil || start != nil || end != nil {
			return errors.New("date-only values require a calendar date and unknown timezone without invented timestamps")
		}
		return nil
	}
	if zone != "" {
		return errors.New("timezone belongs to a calendar-date value")
	}
	if observed != nil {
		if !signalTime(observed) || start != nil || end != nil {
			return errors.New("a sample requires only its actual observation time")
		}
		return nil
	}
	if !signalTime(start) || !signalTime(end) || !end.After(*start) || end.Sub(*start) > 366*24*time.Hour {
		return errors.New("an aggregate requires an ordered period of at most 366 days")
	}
	return nil
}

func (o SignalObservation) Validate() error {
	unit, known := signalUnit(o.Metric, o.Statistic)
	if !validSignalKey(o.Key) || !known || o.Unit != unit || !finiteSignal(o.Value) {
		return errors.New("invalid signal key, metric, canonical unit or value")
	}
	switch o.Statistic {
	case "sample", "snapshot", "min", "max", "mean", "sum", "count":
	default:
		return errors.New("invalid signal statistic")
	}
	switch o.Quality {
	case "measured", "estimated", "derived":
	default:
		return errors.New("invalid signal quality")
	}
	if (o.Statistic == "sample") != (o.ObservedAt != nil) {
		return errors.New("only samples have an observedAt timestamp")
	}
	if err := signalWindow(o.ObservedAt, o.PeriodStart, o.PeriodEnd, o.CalendarDate, o.Timezone); err != nil {
		return err
	}
	if o.Metric == "driving_distance_km" {
		if o.Statistic != "sum" || o.Quality != "estimated" || o.PeriodStart == nil || o.PeriodEnd == nil || o.PeriodEnd.Sub(*o.PeriodStart) > 30*time.Second || o.SourceRevision != nil || o.Value < 0 || o.Value > 400*o.PeriodEnd.Sub(*o.PeriodStart).Hours() {
			return errors.New("driving distance requires an immutable estimated interval of at most 30 seconds within driving speed limits")
		}
	}
	if o.SourceRevision != nil && (o.Statistic == "sample" || !signalTime(o.SourceRevision)) {
		return errors.New("source revisions belong only to aggregates")
	}
	if o.SampleCount != nil && (*o.SampleCount < 0 || *o.SampleCount > 1e9) {
		return errors.New("invalid signal sample count")
	}
	if o.Unit == "count" && (o.Value < 0 || math.Trunc(o.Value) != o.Value) {
		return errors.New("counts must be nonnegative integers")
	}
	if o.Unit == "boolean" && o.Value != 0 && o.Value != 1 {
		return errors.New("boolean signals must be zero or one")
	}
	if o.Statistic != "count" {
		if o.Unit == "%" {
			minimum, maximum := 0.0, 100.0
			if strings.Contains(o.Metric, "_trim_") || o.Metric == "egr_error_pct" {
				minimum = -100
			}
			if o.Metric == "absolute_load_pct" {
				maximum = 25700
			}
			if o.Value < minimum || o.Value > maximum {
				return errors.New("percentage outside its physical range")
			}
		}
		switch o.Unit {
		case "km/h", "km", "kPa", "V", "s", "rpm", "L", "code", "min", "g/s", "L/h", "ratio":
			if o.Value < 0 {
				return errors.New("signal cannot be negative")
			}
		}
		if o.Unit == "code" && math.Trunc(o.Value) != o.Value {
			return errors.New("status codes must be integers")
		}
		if o.Unit == "°C" && o.Value < -273.15 {
			return errors.New("temperature below absolute zero")
		}
	}
	return nil
}

func (c SignalContext) Validate() error {
	if !validSignalKey(c.Key) {
		return errors.New("invalid context key")
	}
	if err := signalWindow(c.ObservedAt, c.PeriodStart, c.PeriodEnd, c.CalendarDate, c.Timezone); err != nil {
		return err
	}
	count := 0
	for _, present := range []bool{c.Coverage != nil, c.Diagnostic != nil, c.Segment != nil, c.Location != nil, c.Snapshot != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("context must contain exactly one typed detail")
	}
	switch c.Kind {
	case "snapshot":
		v := c.Snapshot
		if v == nil || v.Date != c.CalendarDate || v.Timezone != c.Timezone || v.Precision != "day" {
			return errors.New("invalid snapshot context")
		}
	case "coverage":
		v := c.Coverage
		if v == nil || v.RetainedSamples < 0 || v.SkippedIntervals < 0 {
			return errors.New("invalid coverage context")
		}
		if (v.FirstObservedAt == nil) != (v.LastObservedAt == nil) {
			return errors.New("coverage observation bounds must be paired")
		}
		if v.FirstObservedAt != nil && (!signalTime(v.FirstObservedAt) || !signalTime(v.LastObservedAt) || v.LastObservedAt.Before(*v.FirstObservedAt)) {
			return errors.New("invalid coverage observation bounds")
		}
		if v.FirstObservedAt != nil && c.PeriodStart != nil && (v.FirstObservedAt.Before(*c.PeriodStart) || v.LastObservedAt.After(*c.PeriodEnd)) {
			return errors.New("coverage observations outside context period")
		}
	case "diagnostic":
		v := c.Diagnostic
		if v == nil || !validSignalKey(v.Class) || len(v.Class) > 64 || len(v.Codes) > 100 || v.SuccessfulReads < 0 {
			return errors.New("invalid diagnostic context")
		}
		if (v.Class != "stored" && v.Class != "pending" && v.Class != "permanent") || v.Unknown != (v.SuccessfulReads == 0) || (v.Unknown && len(v.Codes) > 0) {
			return errors.New("invalid diagnostic state")
		}
		if !v.Unknown && v.Codes == nil {
			return errors.New("known diagnostic states require an explicit codes array")
		}
		for _, code := range v.Codes {
			if !diagnosticCodePattern.MatchString(code) {
				return errors.New("invalid diagnostic code")
			}
		}
	case "recording_segment":
		v := c.Segment
		if v == nil || !signalTime(&v.StartedAt) || !signalTime(&v.EndedAt) || v.EndedAt.Before(v.StartedAt) || v.Samples < 0 || v.EndedAt.Sub(v.StartedAt) > 366*24*time.Hour {
			return errors.New("invalid recording segment")
		}
		if c.PeriodStart != nil && (v.StartedAt.Before(*c.PeriodStart) || v.EndedAt.After(*c.PeriodEnd)) {
			return errors.New("recording segment outside context period")
		}
		switch v.State {
		case "running", "stopped", "unknown", "transition":
		default:
			return errors.New("invalid recording state")
		}
		for _, value := range []float64{v.SpeedCoverageSeconds, v.RunningSeconds, v.IdleSeconds} {
			if !bounded(value, 366*86400) {
				return errors.New("invalid segment duration")
			}
		}
		for _, value := range []*float64{v.DistanceKM, v.TopSpeedKPH} {
			if value != nil && !bounded(*value, 1e8) {
				return errors.New("invalid segment distance or speed")
			}
		}
	case "location":
		v := c.Location
		if v == nil || !finiteSignal(v.Latitude) || !finiteSignal(v.Longitude) || math.Abs(v.Latitude) > 90 || math.Abs(v.Longitude) > 180 || !validSignalKey(v.Type) || len(v.Type) > 64 {
			return errors.New("invalid location context")
		}
		if v.AccuracyMeters != nil && !bounded(*v.AccuracyMeters, 1e7) {
			return errors.New("invalid location accuracy")
		}
		if v.Type == "gps" && v.hasNativeGPSMetadata() {
			if c.ObservedAt != nil && c.ObservedAt.After(time.Now().Add(time.Minute)) {
				return errors.New("GPS observation is in the future")
			}
			if c.ObservedAt == nil || !validSignalKey(v.RecordingID) || v.FixQuality == nil || (*v.FixQuality != 1 && *v.FixQuality != 2 && *v.FixQuality != 4 && *v.FixQuality != 5) || v.Satellites == nil || *v.Satellites < 3 || *v.Satellites > 99 || v.HDOP == nil || !bounded(*v.HDOP, 50) || *v.HDOP == 0 {
				return errors.New("GPS requires an actual fix time, recording identity and GNSS quality")
			}
			if v.SpeedKPH != nil && !bounded(*v.SpeedKPH, 400) || v.CourseDegrees != nil && (!bounded(*v.CourseDegrees, 360) || *v.CourseDegrees == 360) || v.AltitudeMeters != nil && (!finiteSignal(*v.AltitudeMeters) || *v.AltitudeMeters < -1000 || *v.AltitudeMeters > 20000) {
				return errors.New("invalid GPS motion or altitude")
			}
		} else if v.hasNativeGPSMetadata() {
			return errors.New("GPS metadata requires a GPS location")
		}
	default:
		return errors.New("invalid context kind")
	}
	return nil
}

func (b SignalBatch) Validate() error {
	if !validSignalSource(b.Source) || !validSignalKey(b.BatchID) || len(b.Observations)+len(b.Contexts) == 0 || len(b.Observations) > 2000 || len(b.Contexts) > 500 {
		return errors.New("invalid or oversized signal batch")
	}
	seen := map[string]bool{}
	for _, o := range b.Observations {
		if err := o.Validate(); err != nil {
			return err
		}
		if seen[o.Key] {
			return errors.New("duplicate observation key")
		}
		seen[o.Key] = true
	}
	seen = map[string]bool{}
	for _, c := range b.Contexts {
		if err := c.Validate(); err != nil {
			return err
		}
		if seen[c.Key] {
			return errors.New("duplicate context key")
		}
		seen[c.Key] = true
	}
	return nil
}
