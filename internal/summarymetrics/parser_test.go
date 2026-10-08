package summarymetrics

import (
	"errors"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func smartcarFixture() garage.Record {
	return garage.Record{ID: "synthetic-note", Kind: "note", Title: "Daily Status \u2014 2026-01-02", Tags: []string{"smartcar", "daily-status", "daily-2026-01-02"}, Source: &garage.Source{System: "lubelogger", Collection: "notes", ID: "1"}, Notes: "## Vehicle Status \u2014 2026-01-02\n\n**Odometer:** 1609 km (1000 mi)\n**Fuel Level:** 50% (25.0 L remaining)\n**Estimated Range:** 322 km (200 mi)\n**Oil Life:** 25% ⏳ Getting low\n**Tire Pressures:**\n  - FL: 31.9 PSI (220 kPa)\n  - FR: 31.9 PSI (220 kPa)\n  - RL: 31.9 PSI (220 kPa)\n  - RR: 31.9 PSI (220 kPa)\n**Location:** 0.0000, 0.0000 (LAST_PARKED)"}
}

func obdFixture() garage.Record {
	return garage.Record{ID: "synthetic-obd", Kind: "note", Title: "OBD driving summary 2026-01-02 [obd:synthetic:2026-01-02]", Tags: []string{"obd", "summary"}, Source: &garage.Source{System: "lubelogger", Collection: "notes", ID: "2"}, Notes: strings.Join([]string{
		"OBD driving summary for 2026-01-02 (UTC)",
		"Retained samples: 2; observed 2026-01-02T12:00:00Z through 2026-01-02T12:00:10Z",
		historyExplanation, distanceExplanation,
		"Speed coverage: 10.0 s; observed moving time (both endpoints moving): 10.0 s; skipped discontinuities: 0",
		"Observed engine-running time (both endpoints running): 10.0 s; observed idle time (running and both speeds zero): 0.0 s",
		"ESTIMATED distance over observed speed coverage: 0.062 mi (0.100 km)",
		"Mean speed over observed speed duration, including stops: 22.37 mph (36.00 km/h)",
		"Top observed speed: 22.37 mph (36.00 km/h)", "",
		"Reading ranges: minimum / maximum / latest (latest timestamp UTC; sample count)",
		"Coolant temperature: 20 / 30 / 30 °C (2026-01-02T12:00:10Z; n=2)",
		"Engine RPM: 1000 / 1200 / 1200 rpm (2026-01-02T12:00:10Z; n=2)",
		"Vehicle speed: 36 / 36 / 36 km/h (2026-01-02T12:00:10Z; n=2)", "",
		"Trouble codes observed during this day; later absence never implies a code cleared:",
		"Stored trouble codes: P0100; successful reads=2",
		"Pending trouble codes: none reported in successful reads; successful reads=1",
		"Permanent trouble codes: unknown (no successful reads); successful reads=0", "",
		"Recording segments: 1 (split on reboot, sample gaps or engine-state changes; not guaranteed whole trips)",
		"1. 2026-01-02T12:00:00Z to 2026-01-02T12:00:10Z; engine running; samples=2; estimated distance 0.062 mi; speed coverage 10.0 s; running 10.0 s; idle 0.0 s; top 22.37 mph",
	}, "\n")}
}

func TestSmartcarSnapshotHasNoInventedObservationTime(t *testing.T) {
	b, e := Parse(smartcarFixture())
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Observations) != 9 || len(b.Contexts) != 2 {
		t.Fatalf("unexpected conversion counts: %d %d", len(b.Observations), len(b.Contexts))
	}
	for _, o := range b.Observations {
		if o.ObservedAt != nil || o.PeriodStart != nil || o.PeriodEnd != nil || o.CalendarDate != "2026-01-02" || o.Timezone != "unknown" || o.Statistic != "snapshot" {
			t.Fatal("snapshot manufactured a measurement time")
		}
	}
	if b.Contexts[0].Location == nil || b.Contexts[1].Snapshot == nil {
		t.Fatal("missing location or day precision")
	}
}

func TestOBDPreservesObservedTimesRangesAndDiagnosticKnowledge(t *testing.T) {
	b, e := Parse(obdFixture())
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Observations) != 18 || len(b.Contexts) != 5 {
		t.Fatalf("unexpected conversion counts: %d %d", len(b.Observations), len(b.Contexts))
	}
	for _, o := range b.Observations {
		if o.Statistic == "sample" {
			if o.ObservedAt == nil || o.ObservedAt.Format("15:04:05") != "12:00:10" || o.PeriodStart != nil || o.SampleCount == nil || *o.SampleCount != 2 {
				t.Fatal("latest reading lost provenance")
			}
		} else if o.PeriodStart == nil || o.PeriodEnd == nil || o.ObservedAt != nil {
			t.Fatal("aggregate converted into sample")
		}
	}
	if b.Contexts[1].Diagnostic.Unknown || len(b.Contexts[1].Diagnostic.Codes) != 1 || b.Contexts[2].Diagnostic.Unknown || len(b.Contexts[2].Diagnostic.Codes) != 0 || !b.Contexts[3].Diagnostic.Unknown {
		t.Fatal("diagnostic unknown and empty semantics changed")
	}
	segment := b.Contexts[4].Segment
	if segment == nil || segment.DistanceKM == nil || segment.TopSpeedKPH == nil || segment.State != "running" {
		t.Fatal("recording segment missing")
	}
	second, e := Parse(obdFixture())
	if e != nil || second.BatchID != b.BatchID || second.Observations[0].Key != b.Observations[0].Key {
		t.Fatal("conversion not deterministic")
	}
}

func TestRejectsUnknownOrModifiedNotesWithoutPartialBatch(t *testing.T) {
	cases := map[string]func(*garage.Record){
		"user prose":          func(r *garage.Record) { r.Notes += "\nMy mechanic says to inspect it" },
		"custom title":        func(r *garage.Record) { r.Title = "My observations" },
		"custom tag":          func(r *garage.Record) { r.Tags = append(r.Tags, "personal") },
		"pinned":              func(r *garage.Record) { r.Pinned = true },
		"cost":                func(r *garage.Record) { r.CostCents = 1 },
		"extra field":         func(r *garage.Record) { r.ExtraFields = []garage.ExtraField{{Name: "custom", Value: "keep"}} },
		"changed unit":        func(r *garage.Record) { r.Notes = strings.Replace(r.Notes, "30 °C", "30 °F", 1) },
		"range contradiction": func(r *garage.Record) { r.Notes = strings.Replace(r.Notes, "20 / 30 / 30", "40 / 30 / 30", 1) },
		"wrong day": func(r *garage.Record) {
			r.Notes = strings.Replace(r.Notes, "2026-01-02T12:00:10Z; n=2", "2026-01-03T12:00:10Z; n=2", 1)
		},
		"unknown means empty": func(r *garage.Record) {
			r.Notes = strings.Replace(r.Notes, "unknown (no successful reads); successful reads=0", "unknown (no successful reads); successful reads=1", 1)
		},
		"missing segment": func(r *garage.Record) { r.Notes = r.Notes[:strings.LastIndex(r.Notes, "\n")] },
		"omitted segment": func(r *garage.Record) {
			r.Notes += "\n1 additional segments omitted from this note; all segments remain included in totals and retained events."
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := obdFixture()
			change(&r)
			b, e := Parse(r)
			if !errors.Is(e, ErrUnsupported) || len(b.Observations) != 0 || len(b.Contexts) != 0 {
				t.Fatal("unsupported note returned a partial conversion")
			}
		})
	}
}

func TestUnknownDistanceRemainsAbsent(t *testing.T) {
	r := obdFixture()
	r.Notes = strings.Replace(r.Notes, "Speed coverage: 10.0 s; observed moving time (both endpoints moving): 10.0 s", "Speed coverage: 0.0 s; observed moving time (both endpoints moving): 0.0 s", 1)
	r.Notes = strings.Replace(r.Notes, "ESTIMATED distance over observed speed coverage: 0.062 mi (0.100 km)\nMean speed over observed speed duration, including stops: 22.37 mph (36.00 km/h)", "Estimated distance and mean speed: unknown (no contiguous speed coverage)", 1)
	r.Notes = strings.Replace(r.Notes, "estimated distance 0.062 mi; speed coverage 10.0 s", "estimated distance unknown; speed coverage 0.0 s", 1)
	b, e := Parse(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range b.Observations {
		if o.Metric == "distance_km" || o.Statistic == "mean" {
			t.Fatal("unknown value converted to zero")
		}
	}
	if b.Contexts[4].Segment.DistanceKM != nil {
		t.Fatal("unknown segment distance converted to zero")
	}
}

func TestSmartcarRejectsUnsupportedAndInconsistentFields(t *testing.T) {
	changes := []func(*garage.Record){
		func(r *garage.Record) { r.Notes += "\n**Gear:** PARK" },
		func(r *garage.Record) { r.Notes = strings.Replace(r.Notes, "50% (25.0", "150% (25.0", 1) },
		func(r *garage.Record) {
			r.Notes = strings.Replace(r.Notes, "1609 km (1000 mi)", "1609 km (9000 mi)", 1)
		},
		func(r *garage.Record) {
			r.Notes = strings.Replace(r.Notes, "31.9 PSI (220 kPa)", "31.9 PSI (900 kPa)", 1)
		},
		func(r *garage.Record) { r.Notes = strings.Replace(r.Notes, "0.0000, 0.0000", "91.0000, 0.0000", 1) },
		func(r *garage.Record) { r.Notes += "\n**Oil Life:** 25% ⏳ Getting low" },
	}
	for i, change := range changes {
		r := smartcarFixture()
		change(&r)
		b, e := Parse(r)
		if !errors.Is(e, ErrUnsupported) || len(b.Observations) != 0 {
			t.Fatalf("change %d accepted or partially returned", i)
		}
	}
}

func TestHeaderOnlySnapshotDoesNotInventValues(t *testing.T) {
	r := smartcarFixture()
	r.Notes = strings.Split(r.Notes, "\n")[0]
	b, e := Parse(r)
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Observations) != 0 || len(b.Contexts) != 1 || b.Contexts[0].Snapshot == nil {
		t.Fatal("empty historical snapshot created fake measurements")
	}
}

func TestTransitionSegmentRetainsItsDistinctMeaning(t *testing.T) {
	r := obdFixture()
	r.Notes = strings.Replace(r.Notes, "; engine running; samples=2", "; engine-state transition; samples=0", 1)
	b, e := Parse(r)
	if e != nil {
		t.Fatal(e)
	}
	if b.Contexts[4].Segment.State != "transition" {
		t.Fatal("transition collapsed into unknown")
	}
}
