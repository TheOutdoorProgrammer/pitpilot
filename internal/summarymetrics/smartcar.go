package summarymetrics

import (
	"math"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func (p *parser) smartcar(record garage.Record) error {
	// Match the historical punctuation without introducing it into new output.
	separator := " \u2014 "
	m := match(`## Vehicle Status`+separator+`([0-9]{4}-[0-9]{2}-[0-9]{2})`, p.line())
	if m == nil || p.day(m[1]) != nil {
		return ErrUnsupported
	}
	day := m[1]
	if record.Title != "Daily Status"+separator+day || !validTags(record.Tags, "smartcar", "daily-status", "daily-"+day) || !p.exact("") {
		return ErrUnsupported
	}
	seen := map[string]bool{}
	tires := false
	for p.index < len(p.lines) {
		line := p.line()
		switch {
		case strings.HasPrefix(line, "**Odometer:**"), strings.HasPrefix(line, "**Estimated Range:**"):
			m = match(`\*\*(Odometer|Estimated Range):\*\* ([0-9]+) km \(([0-9]+) mi\)`, line)
			if m == nil || math.Abs(number(m[2])*.621371-number(m[3])) > .82 {
				return ErrUnsupported
			}
			metric, quality := "odometer_km", "measured"
			if m[1] == "Estimated Range" {
				metric, quality = "range_km", "estimated"
			}
			if seen[metric] {
				return ErrUnsupported
			}
			seen[metric] = true
			p.snapshot(metric, "km", quality, number(m[2]))
		case strings.HasPrefix(line, "**Fuel Level:**"):
			m = match(`\*\*Fuel Level:\*\* ([0-9]+)%(?: \(([0-9]+\.[0-9]+) L remaining\))?`, line)
			if m == nil || seen["fuel"] || number(m[1]) > 100 {
				return ErrUnsupported
			}
			seen["fuel"] = true
			p.snapshot("fuel_level_pct", "%", "measured", number(m[1]))
			if m[2] != "" {
				p.snapshot("fuel_remaining_l", "L", "measured", number(m[2]))
			}
		case strings.HasPrefix(line, "**Oil Life:**"):
			m = match(`\*\*Oil Life:\*\* ([0-9]+)%(.*)`, line)
			if m == nil || seen["oil"] || number(m[1]) > 100 {
				return ErrUnsupported
			}
			seen["oil"] = true
			// Rounding can move a value across the displayed warning threshold.
			v := number(m[1])
			suffix := m[2]
			valid := suffix == "" && v >= 30 || suffix == " ⏳ Getting low" && v >= 15 && v <= 30 || suffix == " ⚠️ CHANGE SOON" && v <= 15
			if !valid {
				return ErrUnsupported
			}
			p.snapshot("oil_life_pct", "%", "measured", v)
		case line == "**Tire Pressures:**":
			if tires {
				return ErrUnsupported
			}
			tires = true
		case strings.HasPrefix(line, "  - "):
			m = match(`  - (FL|FR|RL|RR): ([0-9]+\.[0-9]+) PSI \(([0-9]+) kPa\)`, line)
			if m == nil || !tires || seen[m[1]] || math.Abs(number(m[3])*.145038-number(m[2])) > .13 {
				return ErrUnsupported
			}
			seen[m[1]] = true
			// PSI is printed to 0.1, preserving more precision than whole kPa.
			p.snapshot("tire_"+strings.ToLower(m[1])+"_kpa", "kPa", "measured", number(m[2])/.145038)
		case strings.HasPrefix(line, "**Location:**"):
			m = match(`\*\*Location:\*\* `+numeric+`, `+numeric+` \((LAST_PARKED|LIVE|CURRENT)\)`, line)
			if m == nil || seen["location"] || math.Abs(number(m[1])) > 90 || math.Abs(number(m[2])) > 180 {
				return ErrUnsupported
			}
			seen["location"] = true
			p.batch.Contexts = append(p.batch.Contexts, garage.SignalContext{Key: p.contextKey("location"), Kind: "location", CalendarDate: day, Timezone: "unknown", Location: &garage.SignalLocation{Latitude: number(m[1]), Longitude: number(m[2]), Type: m[3]}})
		default:
			return ErrUnsupported
		}
	}
	if tires && !seen["FL"] && !seen["FR"] && !seen["RL"] && !seen["RR"] {
		return ErrUnsupported
	}
	p.batch.Contexts = append(p.batch.Contexts, garage.SignalContext{Key: p.contextKey("snapshot"), Kind: "snapshot", CalendarDate: day, Timezone: "unknown", Snapshot: &garage.SignalSnapshot{Date: day, Timezone: "unknown", Precision: "day"}})
	return nil
}

func (p *parser) snapshot(metric, unit, quality string, value float64) {
	p.observation(metric, unit, "snapshot", quality, value, nil)
	o := &p.batch.Observations[len(p.batch.Observations)-1]
	o.PeriodStart, o.PeriodEnd = nil, nil
	o.CalendarDate, o.Timezone = p.start.Format(time.DateOnly), "unknown"
}
