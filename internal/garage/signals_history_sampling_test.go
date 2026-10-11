package garage

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestSignalHistorySparseDrivesSpendBudgetOnReadings(t *testing.T) {
	s, id := signalFixture(t)
	start := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "sparse-drives"}
	var samples []SignalObservation
	for segment, run := range []struct {
		offset time.Duration
		count  int
	}{{0, 53}, {24 * time.Hour, 150}, {44 * time.Hour, 367}, {46 * time.Hour, 316}} {
		for i := range run.count {
			o := signalSample(fmt.Sprintf("drive-%d-%03d", segment, i), float64((i*31+segment*7)%99), start.Add(run.offset+time.Duration(i)*5*time.Second))
			o.Metric, o.Unit = "timing_advance_deg", "deg"
			samples = append(samples, o)
		}
	}
	batch.Observations = samples
	ingestFixture(t, s, id, batch)
	before, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	q := SignalHistoryQuery{Metric: "timing_advance_deg", Statistic: "sample", From: start.Add(-27 * 24 * time.Hour), To: start.Add(3 * 24 * time.Hour), MaxPoints: 120}
	history, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || len(history.Series) != 1 {
		t.Fatalf("sparse history: %v %+v", err, history)
	}
	series := history.Series[0]
	if series.Source != "pi" || series.Quality != "measured" || series.Statistic != "sample" || series.Unit != "deg" || len(series.Points) != 120 {
		t.Fatalf("unused budget or lost provenance: %+v", series)
	}
	consumed := 0
	for _, p := range series.Points {
		if p.Count < 1 || consumed+p.Count > len(samples) {
			t.Fatalf("invalid sample coverage at %d: %+v", consumed, p)
		}
		covered := samples[consumed : consumed+p.Count]
		minimum, maximum, mean := covered[0], covered[0], 0.0
		for _, sample := range covered {
			mean += sample.Value / float64(len(covered))
			if sample.Value < minimum.Value {
				minimum = sample
			}
			if sample.Value > maximum.Value {
				maximum = sample
			}
		}
		first, last := covered[0], covered[len(covered)-1]
		if p.First != first.Value || p.Last != last.Value || p.Minimum != minimum.Value || p.Maximum != maximum.Value || math.Abs(p.Mean-mean) > 1e-10 || !p.FirstObservedAt.Equal(*first.ObservedAt) || !p.LastObservedAt.Equal(*last.ObservedAt) || !p.MinimumObservedAt.Equal(*minimum.ObservedAt) || !p.MaximumObservedAt.Equal(*maximum.ObservedAt) {
			t.Fatalf("lost exact readings at %d: %+v", consumed, p)
		}
		if !p.BucketStart.Equal(*p.FirstObservedAt) || !p.BucketEnd.Equal(*p.LastObservedAt) || !p.WindowStart.Equal(*p.FirstObservedAt) || !p.WindowEnd.Equal(*p.LastObservedAt) || p.MaxGapSeconds == nil || *p.MaxGapSeconds > 5 {
			t.Fatalf("bucket crossed a recording outage or invented coverage: %+v", p)
		}
		consumed += p.Count
	}
	if consumed != len(samples) || consumed != 886 {
		t.Fatalf("covered %d samples, want886", consumed)
	}
	q.From = start
	q.To = start.Add(47 * time.Hour)
	narrower, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || !reflect.DeepEqual(narrower.Series, history.Series) {
		t.Fatal("empty requested time changed the recorded detail", err)
	}
	assertConversionUnchanged(t, s, before)
}

func TestSignalHistoryPreservesRawSamplesWithinBudget(t *testing.T) {
	s, id := signalFixture(t)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "raw-budget"}
	for i := range 24 {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprintf("sample-%02d", i), float64(i), start.Add(time.Duration(i/2)*time.Second)))
	}
	batch.Observations = append(batch.Observations, signalSample("outside-from", 100, start.Add(-time.Nanosecond)), signalSample("outside-to", 100, start.Add(24*time.Second)))
	ingestFixture(t, s, id, batch)
	q := SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "all", From: start, To: start.Add(24 * time.Second), MaxPoints: 120}
	history, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || len(history.Series) != 1 || len(history.Series[0].Points) != 24 {
		t.Fatalf("raw history: %v %+v", err, history)
	}
	for i, p := range history.Series[0].Points {
		want := batch.Observations[i]
		if p.BucketIndex == nil || *p.BucketIndex != i || p.Count != 1 || p.First != want.Value || p.Last != want.Value || p.Minimum != want.Value || p.Maximum != want.Value || p.Mean != want.Value || p.MaxGapSeconds == nil || *p.MaxGapSeconds != 0 || !p.FirstObservedAt.Equal(*want.ObservedAt) || !p.LastObservedAt.Equal(*want.ObservedAt) {
			t.Fatalf("raw reading changed: %+v", p)
		}
	}
	repeated, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || !reflect.DeepEqual(history, repeated) {
		t.Fatal("same-timestamp bucket identities changed across identical queries", err)
	}
}

func TestSignalHistoryMoreSessionsThanBudgetPreservesGapsAndCoverage(t *testing.T) {
	s, id := signalFixture(t)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "many-sessions"}
	for i := range 10 {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprintf("sample-%02d", i), float64(i), start.Add(time.Duration(i)*time.Hour)))
	}
	ingestFixture(t, s, id, batch)
	for _, budget := range []int{1, 3, 10} {
		q := SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", From: start, To: start.Add(30 * 24 * time.Hour), MaxPoints: budget}
		history, err := s.SignalHistory(context.Background(), id, q)
		if err != nil || len(history.Series) != 1 || len(history.Series[0].Points) != budget {
			t.Fatalf("budget%d: %v %+v", budget, err, history)
		}
		count := 0
		for i, p := range history.Series[0].Points {
			wantCount := 1
			if i == budget-1 {
				wantCount = 10 - (budget - 1)
			}
			wantGap := 0.0
			if wantCount > 1 {
				wantGap = 3600
			}
			if p.Count != wantCount || p.MaxGapSeconds == nil || *p.MaxGapSeconds != wantGap {
				t.Fatalf("lost gaps or unstable equal-gap boundary, budget%d point%d: %+v", budget, i, p)
			}
			count += p.Count
		}
		if count != 10 {
			t.Fatalf("covered%d readings, want10", count)
		}
	}
}

func TestSignalHistoryLimitedBudgetKeepsLargestGapDeterministically(t *testing.T) {
	s, id := signalFixture(t)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "largest-gap"}
	at := start
	for i, gap := range []time.Duration{0, 20 * time.Minute, time.Hour, 30 * time.Minute, 90 * time.Minute, 90 * time.Minute} {
		at = at.Add(gap)
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprintf("sample-%02d", i), float64(i), at))
	}
	// Equal timestamps must still count as independent retained observations.
	batch.Observations = append(batch.Observations, signalSample("same-time", 99, at))
	ingestFixture(t, s, id, batch)
	q := SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", From: start, To: start.Add(24 * time.Hour), MaxPoints: 2}
	history, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || len(history.Series) != 1 || len(history.Series[0].Points) != 2 {
		t.Fatalf("history: %v %+v", err, history)
	}
	points := history.Series[0].Points
	if points[0].Count != 4 || points[1].Count != 3 || points[0].Last != 3 || points[1].First != 4 || *points[0].MaxGapSeconds != 3600 || *points[1].MaxGapSeconds != 5400 || points[1].Maximum != 99 || !points[1].MaximumObservedAt.Equal(at) {
		t.Fatalf("largest gap, exact timestamp tie or gap metadata lost: %+v", points)
	}
	repeated, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || !reflect.DeepEqual(history, repeated) {
		t.Fatal("repeated query changed same-time readings or equal-gap boundaries", err)
	}
}
