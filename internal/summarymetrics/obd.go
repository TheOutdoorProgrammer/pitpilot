package summarymetrics

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

const historyExplanation = "Rebuilt from all retained receiver history, including earlier snapshots. Unknown-clock events cannot be assigned to a day."
const distanceExplanation = "Estimated distance integrates speed only between consecutive samples from the same boot, at most 30 seconds apart, with consistent clocks. Missing coverage is excluded, not extrapolated. This is not dashboard odometer mileage or a guaranteed whole trip."
const fuelExplanation = "Fuel-system status: 1=cold/open loop; 2=closed loop; 4=load/deceleration open loop; 8=fault/open loop; 16=closed loop with feedback fault."
const readinessExplanation = "Readiness monitor values: 1=complete; 0=incomplete. Absent monitors are unknown or unsupported."

func (p *parser) obd(record garage.Record) error {
	m := match(`OBD driving summary for ([0-9]{4}-[0-9]{2}-[0-9]{2}) \(UTC\)`, p.line())
	if m == nil || p.day(m[1]) != nil {
		return ErrUnsupported
	}
	day := m[1]
	title := obdTitle.FindStringSubmatch(record.Title)
	if title == nil || title[1] != day || title[2] != day || !(validTags(record.Tags, "obd", "summary") || validTags(record.Tags, "obd", "snapshot")) {
		return ErrUnsupported
	}
	m = match(`Retained samples: ([0-9]+); observed (\S+) through (\S+)`, p.line())
	if m == nil || integer(m[1]) <= 0 {
		return ErrUnsupported
	}
	samples := integer(m[1])
	first, e := p.stamp(m[2])
	if e != nil {
		return e
	}
	last, e := p.stamp(m[3])
	if e != nil || last.Before(*first) {
		return ErrUnsupported
	}
	p.observation("retained_samples", "count", "count", "derived", float64(samples), nil)
	if !p.exact(historyExplanation) || !p.exact(distanceExplanation) {
		return ErrUnsupported
	}
	if e = p.dailyTotals(); e != nil {
		return e
	}
	skipped := 0
	for _, o := range p.batch.Observations {
		if o.Metric == "skipped_intervals" {
			skipped = int(o.Value)
		}
	}
	p.batch.Contexts = append(p.batch.Contexts, garage.SignalContext{Key: p.contextKey("coverage"), Kind: "coverage", PeriodStart: &p.start, PeriodEnd: &p.end, Coverage: &garage.SignalCoverage{FirstObservedAt: first, LastObservedAt: last, RetainedSamples: samples, SkippedIntervals: skipped}})
	if e = p.parseRanges(); e != nil {
		return e
	}
	if !p.exact("") || !p.exact("Trouble codes observed during this day; later absence never implies a code cleared:") {
		return ErrUnsupported
	}
	for _, class := range []string{"Stored", "Pending", "Permanent"} {
		m = match(class+` trouble codes: (.+); successful reads=([0-9]+)`, p.line())
		if m == nil {
			return ErrUnsupported
		}
		d := &garage.SignalDiagnostic{Class: strings.ToLower(class), SuccessfulReads: integer(m[2]), Codes: []string{}}
		switch m[1] {
		case "unknown (no successful reads)":
			d.Unknown = true
			if d.SuccessfulReads != 0 {
				return ErrUnsupported
			}
		case "none reported in successful reads":
			if d.SuccessfulReads <= 0 {
				return ErrUnsupported
			}
		default:
			if d.SuccessfulReads <= 0 {
				return ErrUnsupported
			}
			seen := map[string]bool{}
			for _, code := range strings.Split(m[1], ", ") {
				if match(`[PBCU][0-3][0-9A-F]{3}`, code) == nil || seen[code] {
					return ErrUnsupported
				}
				seen[code] = true
				d.Codes = append(d.Codes, code)
			}
		}
		p.batch.Contexts = append(p.batch.Contexts, garage.SignalContext{Key: p.contextKey("diagnostic"), Kind: "diagnostic", PeriodStart: &p.start, PeriodEnd: &p.end, Diagnostic: d})
	}
	if !p.exact("") {
		return ErrUnsupported
	}
	m = match(`Recording segments: ([0-9]+) \(split on reboot, sample gaps or engine-state changes; not guaranteed whole trips\)`, p.line())
	if m == nil {
		return ErrUnsupported
	}
	segments := integer(m[1])
	if segments <= 0 || segments > 100 {
		return ErrUnsupported
	}
	for i := 0; i < segments; i++ {
		if e = p.segment(i + 1); e != nil {
			return e
		}
	}
	return nil
}

func (p *parser) segment(index int) error {
	m := match(`([0-9]+)\. (\S+) to (\S+); (engine running|engine stopped|engine state unknown|engine-state transition); samples=([0-9]+); estimated distance (unknown|[0-9]+\.[0-9]+ mi); speed coverage `+numeric+` s; running `+numeric+` s; idle `+numeric+` s; top (unknown|[0-9]+\.[0-9]+ mph)`, p.line())
	if m == nil || integer(m[1]) != index {
		return ErrUnsupported
	}
	start, e := time.Parse(time.RFC3339, m[2])
	if e != nil {
		return ErrUnsupported
	}
	end, e := time.Parse(time.RFC3339, m[3])
	if e != nil || end.Before(start) || start.Before(p.start) || end.After(p.end) {
		return ErrUnsupported
	}
	state := map[string]string{"engine running": "running", "engine stopped": "stopped", "engine state unknown": "unknown", "engine-state transition": "transition"}[m[4]]
	s := &garage.SignalSegment{StartedAt: start, EndedAt: end, State: state, Samples: integer(m[5]), SpeedCoverageSeconds: number(m[7]), RunningSeconds: number(m[8]), IdleSeconds: number(m[9])}
	if s.Samples < 0 || s.SpeedCoverageSeconds < 0 || s.RunningSeconds < 0 || s.IdleSeconds < 0 || s.IdleSeconds > s.RunningSeconds {
		return ErrUnsupported
	}
	if m[6] != "unknown" {
		v := number(strings.TrimSuffix(m[6], " mi")) * 1.609344
		s.DistanceKM = &v
	}
	if m[10] != "unknown" {
		v := number(strings.TrimSuffix(m[10], " mph")) * 1.609344
		s.TopSpeedKPH = &v
	}
	if (s.SpeedCoverageSeconds > 0) != (s.DistanceKM != nil) {
		return ErrUnsupported
	}
	p.batch.Contexts = append(p.batch.Contexts, garage.SignalContext{Key: p.contextKey("recording_segment"), Kind: "recording_segment", PeriodStart: &p.start, PeriodEnd: &p.end, Segment: s})
	return nil
}

func (p *parser) parseRanges() error {
	if !p.exact("") || !p.exact("Reading ranges: minimum / maximum / latest (latest timestamp UTC; sample count)") {
		return ErrUnsupported
	}
	seen := map[string]bool{}
	fuel, ready := false, false
	for p.index < len(p.lines) {
		m := match(`([^:]+): `+numeric+` / `+numeric+` / `+numeric+` (.*?) \(([^;]+); n=([0-9]+)\)`, p.lines[p.index])
		if m == nil {
			break
		}
		p.index++
		r, ok := readings[m[1]]
		if !ok || seen[r.metric] || r.textUnit != m[5] {
			return ErrUnsupported
		}
		seen[r.metric] = true
		min, max, latest := number(m[2]), number(m[3]), number(m[4])
		n := integer(m[7])
		at, e := p.stamp(m[6])
		if e != nil || n <= 0 || min > max || latest < min || latest > max || math.IsNaN(min+max+latest) {
			return ErrUnsupported
		}
		p.observation(r.metric, r.unit, "min", "measured", min, nil)
		p.observation(r.metric, r.unit, "max", "measured", max, nil)
		p.observation(r.metric, r.unit, "sample", "measured", latest, at)
		for i := len(p.batch.Observations) - 3; i < len(p.batch.Observations); i++ {
			p.batch.Observations[i].SampleCount = &n
		}
		fuel = fuel || strings.HasPrefix(r.metric, "fuel_system_")
		ready = ready || (strings.HasPrefix(r.metric, "readiness_") && strings.HasSuffix(r.metric, "_ready"))
	}
	if len(seen) == 0 {
		return ErrUnsupported
	}
	if fuel && !p.exact(fuelExplanation) {
		return ErrUnsupported
	}
	if ready && !p.exact(readinessExplanation) {
		return ErrUnsupported
	}
	return nil
}

func paired(m []string, precision float64) bool {
	if m == nil {
		return false
	}
	a, b := number(m[1]), number(m[2])
	return a >= 0 && b >= 0 && math.Abs(a*1.609344-b) <= precision
}

func (p *parser) dailyTotals() error {
	m := match(`Speed coverage: `+numeric+` s; observed moving time \(both endpoints moving\): `+numeric+` s; skipped discontinuities: ([0-9]+)`, p.line())
	if m == nil {
		return ErrUnsupported
	}
	coverage, moving, skipped := number(m[1]), number(m[2]), integer(m[3])
	if coverage < 0 || moving < 0 || moving > coverage || skipped < 0 {
		return ErrUnsupported
	}
	p.observation("speed_coverage_s", "s", "sum", "derived", coverage, nil)
	p.observation("moving_time_s", "s", "sum", "derived", moving, nil)
	p.observation("skipped_intervals", "count", "count", "derived", float64(skipped), nil)
	m = match(`Observed engine-running time \(both endpoints running\): `+numeric+` s; observed idle time \(running and both speeds zero\): `+numeric+` s`, p.line())
	if m == nil || number(m[1]) < 0 || number(m[2]) < 0 || number(m[2]) > number(m[1]) {
		return ErrUnsupported
	}
	p.observation("running_time_s", "s", "sum", "derived", number(m[1]), nil)
	p.observation("idle_time_s", "s", "sum", "derived", number(m[2]), nil)
	if coverage > 0 {
		m = match(`ESTIMATED distance over observed speed coverage: `+numeric+` mi \(`+numeric+` km\)`, p.line())
		if !paired(m, .002) {
			return ErrUnsupported
		}
		p.observation("distance_km", "km", "sum", "estimated", number(m[2]), nil)
		m = match(`Mean speed over observed speed duration, including stops: `+numeric+` mph \(`+numeric+` km/h\)`, p.line())
		if !paired(m, .02) {
			return ErrUnsupported
		}
		p.observation("speed_kph", "km/h", "mean", "derived", number(m[2]), nil)
	} else if !p.exact("Estimated distance and mean speed: unknown (no contiguous speed coverage)") {
		return ErrUnsupported
	}
	if p.index < len(p.lines) && strings.HasPrefix(p.lines[p.index], "Top observed speed:") {
		m = match(`Top observed speed: `+numeric+` mph \(`+numeric+` km/h\)`, p.line())
		if !paired(m, .02) {
			return ErrUnsupported
		}
		// A separate series avoids competing with the more precise range max.
		p.observation("daily_top_speed_kph", "km/h", "max", "derived", number(m[2]), nil)
	}
	return nil
}

var obdTitle = regexp.MustCompile(`^OBD driving summary ([0-9]{4}-[0-9]{2}-[0-9]{2}) \[obd:[a-zA-Z0-9_-]+:([0-9]{4}-[0-9]{2}-[0-9]{2})\]$`)

func (p *parser) contextKey(kind string) string {
	return fmt.Sprintf("%s-%s-%04d", p.batch.BatchID, kind, len(p.batch.Contexts))
}
