package garage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func signalFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s := importStore(t)
	id := NewID()
	if err := s.CreateVehicle(context.Background(), Vehicle{ID: id, Name: "Synthetic signals vehicle", Year: 2020}); err != nil {
		t.Fatal(err)
	}
	return s, id
}
func signalSample(key string, value float64, at time.Time) SignalObservation {
	return SignalObservation{Key: key, Metric: "manifold_kpa", Unit: "kPa", Statistic: "sample", Quality: "measured", Value: value, ObservedAt: &at}
}
func ingestFixture(t *testing.T, s *Store, id string, b SignalBatch) SignalIngestReport {
	t.Helper()
	report, err := s.IngestSignals(context.Background(), id, b)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestSignalsIdempotencyRevisionAndAtomicity(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "batch1", Observations: []SignalObservation{signalSample("sample", 0, at)}}
	if got := ingestFixture(t, s, id, batch); got.Created != 1 {
		t.Fatal(got)
	}
	if got := ingestFixture(t, s, id, batch); got.Skipped != 1 {
		t.Fatal(got)
	}
	before, _ := s.Export(ctx)
	bad := batch
	bad.Observations = []SignalObservation{signalSample("new", 9, at), signalSample("sample", 8, at)}
	bad.BatchID = "batch2"
	if _, err := s.IngestSignals(ctx, id, bad); !errors.Is(err, ErrSignalConflict) {
		t.Fatal(err)
	}
	assertConversionUnchanged(t, s, before)
	bad.BatchID = "batch1"
	if _, err := s.IngestSignals(ctx, id, bad); !errors.Is(err, ErrSignalConflict) {
		t.Fatal(err)
	}
	end := at.Add(time.Hour)
	revision := end.Add(time.Hour)
	aggregate := SignalObservation{Key: "aggregate", Metric: "manifold_kpa", Unit: "kPa", Statistic: "max", Quality: "measured", Value: 70, PeriodStart: &at, PeriodEnd: &end, SourceRevision: &revision}
	batch = SignalBatch{Source: "pi", BatchID: "aggregate1", Observations: []SignalObservation{aggregate}}
	ingestFixture(t, s, id, batch)
	newer := revision.Add(time.Hour)
	batch.BatchID = "aggregate2"
	batch.Observations[0].SourceRevision = &newer
	batch.Observations[0].Value = 90
	if got := ingestFixture(t, s, id, batch); got.Updated != 1 {
		t.Fatal(got)
	}
	replay := SignalBatch{Source: "pi", BatchID: "aggregate1", Observations: []SignalObservation{aggregate}}
	if got := ingestFixture(t, s, id, replay); got.Skipped != 1 || got.Updated != 0 {
		t.Fatal("original retry replaced newer aggregate", got)
	}
	batch.BatchID = "aggregate3"
	batch.Observations[0] = aggregate
	if got := ingestFixture(t, s, id, batch); got.Skipped != 1 {
		t.Fatal(got)
	}
	batch.BatchID = "aggregate4"
	batch.Observations[0].SourceRevision = &newer
	batch.Observations[0].Value = 88
	if _, err := s.IngestSignals(ctx, id, batch); !errors.Is(err, ErrSignalConflict) {
		t.Fatal(err)
	}
	batch.BatchID = "aggregate5"
	later := newer.Add(time.Hour)
	batch.Observations[0].SourceRevision = &later
	batch.Observations[0].Quality = "estimated"
	if _, err := s.IngestSignals(ctx, id, batch); !errors.Is(err, ErrSignalConflict) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	before, _ = s.Export(ctx)
	if _, err := s.IngestSignals(canceled, id, batch); err == nil {
		t.Fatal("canceled ingestion accepted")
	}
	assertConversionUnchanged(t, s, before)
	if _, err := s.IngestSignals(ctx, "missing", batch); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.DeleteVehicle(ctx, id); err != nil {
		t.Fatal(err)
	}
	exported, _ := s.Export(ctx)
	if len(exported.Signals)+len(exported.SignalContexts)+len(exported.SignalBatches) != 0 {
		t.Fatal("vehicle deletion retained signal data")
	}
}

func TestSignalsLatestOutOfOrderZeroAndConcurrentReplay(t *testing.T) {
	s, id := signalFixture(t)
	now := time.Now().UTC()
	batch := SignalBatch{Source: "pi", BatchID: "zero", Observations: []SignalObservation{signalSample("zero", 0, now)}}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.IngestSignals(context.Background(), id, batch); failures <- err }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	batch.BatchID = "old"
	batch.Observations = []SignalObservation{signalSample("old", 50, now.Add(-time.Hour))}
	ingestFixture(t, s, id, batch)
	latest, err := s.LatestSignals(context.Background(), id)
	if err != nil || len(latest.Series) != 1 || latest.Series[0].Latest.Value != 0 || latest.Series[0].Stale {
		t.Fatalf("zero/latest semantics: %+v %v", latest.Series, err)
	}
	other := NewID()
	if err = s.CreateVehicle(context.Background(), Vehicle{ID: other, Name: "No readings", Year: 2020}); err != nil {
		t.Fatal(err)
	}
	unknown, err := s.LatestSignals(context.Background(), other)
	if err != nil || len(unknown.Series) != 0 {
		t.Fatal("unknown became zero")
	}
}

func TestSignalHistoryPreservesExtremaQualityAndCalendarPrecision(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "history"}
	for i, v := range []float64{50, 1, 99, 40} {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprint(i), v, start.Add(time.Duration(i)*time.Minute)))
	}
	estimated := signalSample("estimated", 70, start)
	estimated.Quality = "estimated"
	batch.Observations = append(batch.Observations, estimated)
	ingestFixture(t, s, id, batch)
	q := SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", From: start, To: start.Add(time.Hour), MaxPoints: 1}
	history, err := s.SignalHistory(ctx, id, q)
	if err != nil || len(history.Series) != 2 {
		t.Fatalf("history %v %+v", err, history)
	}
	var point SignalHistoryPoint
	for _, series := range history.Series {
		if series.Quality == "measured" {
			point = series.Points[0]
		}
	}
	if point.Minimum != 1 || point.Maximum != 99 || point.First != 50 || point.Last != 40 || point.Mean != 47.5 || point.Count != 4 {
		t.Fatal(point)
	}
	if point.MinimumObservedAt == nil || !point.MinimumObservedAt.Equal(start.Add(time.Minute)) || point.MaximumObservedAt == nil || !point.MaximumObservedAt.Equal(start.Add(2*time.Minute)) || point.MaxGapSeconds == nil || *point.MaxGapSeconds != 60 {
		t.Fatal("downsampling lost peak times or recording gaps", point)
	}
	snapshot := SignalObservation{Key: "day", Metric: "manifold_kpa", Unit: "kPa", Statistic: "snapshot", Quality: "measured", Value: 65, CalendarDate: "2026-06-01", Timezone: "unknown"}
	ingestFixture(t, s, id, SignalBatch{Source: "smartcar", BatchID: "snapshot", Observations: []SignalObservation{snapshot}})
	q.Statistic = "snapshot"
	q.From = start.Add(12 * time.Hour)
	q.To = start.Add(24 * time.Hour)
	history, err = s.SignalHistory(ctx, id, q)
	if err != nil || len(history.Series) != 1 {
		t.Fatalf("calendar day omitted at midday range: %v %+v", err, history)
	}
	point = history.Series[0].Points[0]
	if point.CalendarDate != "2026-06-01" || point.Timezone != "unknown" || point.BucketStart != nil || point.WindowStart != nil || point.FirstObservedAt != nil || point.MinimumObservedAt != nil || point.MaximumObservedAt != nil || point.MaxGapSeconds != nil {
		t.Fatal("calendar precision invented timestamps")
	}
	periodEnd := start.Add(24 * time.Hour)
	aggregate := SignalObservation{Key: "period", Metric: "manifold_kpa", Unit: "kPa", Statistic: "max", Quality: "measured", Value: 100, PeriodStart: &start, PeriodEnd: &periodEnd}
	ingestFixture(t, s, id, SignalBatch{Source: "pi", BatchID: "period", Observations: []SignalObservation{aggregate}})
	q.Statistic = "max"
	history, err = s.SignalHistory(ctx, id, q)
	if err != nil || len(history.Series) != 1 || history.Series[0].Points[0].Maximum != 100 {
		t.Fatal("aggregate ending at query end omitted", err)
	}
	q.To = start.Add(20 * time.Hour)
	history, err = s.SignalHistory(ctx, id, q)
	if err != nil || len(history.Series) != 1 {
		t.Fatal("overlapping aggregate omitted", err)
	}
}

func TestSignalHistoryAllPreservesStatisticsUnitsAndPrecision(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	batch := SignalBatch{Source: "pi", BatchID: "mixed"}
	for i := range 240 {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprint(i), float64(i%100), start.Add(time.Duration(i)*time.Minute)))
	}
	for _, statistic := range []string{"mean", "max", "count"} {
		unit, _ := signalUnit("manifold_kpa", statistic)
		batch.Observations = append(batch.Observations, SignalObservation{Key: statistic, Metric: "manifold_kpa", Unit: unit, Statistic: statistic, Quality: "measured", Value: 90, PeriodStart: &start, PeriodEnd: &end})
	}
	ingestFixture(t, s, id, batch)
	ingestFixture(t, s, id, SignalBatch{Source: "lubelogger", BatchID: "calendar", Observations: []SignalObservation{{Key: "day", Metric: "manifold_kpa", Unit: "kPa", Statistic: "snapshot", Quality: "measured", Value: 42, CalendarDate: "2026-06-01", Timezone: "unknown"}}})
	estimate := signalSample("estimated", 60, start.Add(time.Hour))
	estimate.Quality = "estimated"
	ingestFixture(t, s, id, SignalBatch{Source: "smartcar", BatchID: "estimate", Observations: []SignalObservation{estimate}})
	before, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "all", From: start, To: start.Add(12 * time.Hour), MaxPoints: 3}
	history, err := s.SignalHistory(ctx, id, q)
	if err != nil || len(history.Series) != 6 || history.Unit != "kPa" {
		t.Fatalf("mixed history: %v %+v", err, history)
	}
	for _, series := range history.Series {
		if len(series.Points) > q.MaxPoints {
			t.Fatalf("unbounded timestamp series: %+v", series)
		}
		wantUnit, _ := signalUnit(q.Metric, series.Statistic)
		if series.Unit != wantUnit {
			t.Fatalf("statistic unit changed: %+v", series)
		}
		if series.Statistic == "snapshot" {
			p := series.Points[0]
			if p.CalendarDate != "2026-06-01" || p.Timezone != "unknown" || p.FirstObservedAt != nil || p.BucketStart != nil || p.WindowStart != nil {
				t.Fatalf("invented imported timestamp: %+v", p)
			}
		}
		one := q
		one.Statistic, one.Source, one.Quality = series.Statistic, series.Source, series.Quality
		individual, err := s.SignalHistory(ctx, id, one)
		if err != nil || len(individual.Series) != 1 || !reflect.DeepEqual(individual.Series[0], series) {
			t.Fatalf("combined query changed source values: %v %+v %+v", err, individual, series)
		}
	}
	q.Source, q.Quality = "smartcar", "estimated"
	filtered, err := s.SignalHistory(ctx, id, q)
	if err != nil || len(filtered.Series) != 1 || filtered.Series[0].Source != "smartcar" || filtered.Series[0].Quality != "estimated" {
		t.Fatalf("combined query ignored filters: %v %+v", err, filtered)
	}
	assertConversionUnchanged(t, s, before)
}

func TestSignalContextsAndStrictInput(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC()
	batch := SignalBatch{Source: "pi", BatchID: "diagnostics"}
	for _, class := range []string{"stored", "pending", "permanent"} {
		batch.Contexts = append(batch.Contexts, SignalContext{Key: class, Kind: "diagnostic", ObservedAt: &at, Diagnostic: &SignalDiagnostic{Class: class, SuccessfulReads: 1, Codes: []string{}}})
	}
	ingestFixture(t, s, id, batch)
	latest, err := s.LatestSignals(context.Background(), id)
	if err != nil || len(latest.Contexts) != 3 {
		t.Fatal("diagnostic classes collapsed", err)
	}
	valid := signalSample("key", 45, at)
	raw, _ := json.Marshal(valid)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, value := range []json.RawMessage{nil, json.RawMessage("null")} {
		if value == nil {
			delete(fields, "value")
		} else {
			fields["value"] = value
		}
		bad, _ := json.Marshal(fields)
		var parsed SignalObservation
		if json.Unmarshal(bad, &parsed) == nil {
			t.Fatal("missing or null value accepted as zero")
		}
	}
	for _, change := range []func(*SignalObservation){func(o *SignalObservation) { o.Value = -1 }, func(o *SignalObservation) { o.Unit = "psi" }, func(o *SignalObservation) { o.Metric = "unknown" }, func(o *SignalObservation) { o.ObservedAt = nil }} {
		bad := valid
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid signal accepted")
		}
	}
	exported, _ := s.Export(context.Background())
	if len(exported.SignalContexts) != 3 || len(exported.SignalBatches) != 1 {
		t.Fatal("export missing context or replay ledger")
	}
	original := batch
	if _, err := normalizeSignalBatch(batch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, batch) {
		t.Fatal("normalization mutated caller")
	}
}

func TestSignalsPreserveSubMillisecondObservationOrder(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC().Truncate(time.Second)
	batch := SignalBatch{Source: "pi", BatchID: "precision", Observations: []SignalObservation{signalSample("z-earlier", 1, at.Add(100*time.Nanosecond)), signalSample("a-later", 2, at.Add(900*time.Nanosecond))}}
	ingestFixture(t, s, id, batch)
	latest, err := s.LatestSignals(context.Background(), id)
	if err != nil || len(latest.Series) != 1 || latest.Series[0].Latest.Value != 2 {
		t.Fatal("latest follows key order instead of source time", err)
	}
	history, err := s.SignalHistory(context.Background(), id, SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", From: at, To: at.Add(time.Second), MaxPoints: 1})
	if err != nil {
		t.Fatal(err)
	}
	p := history.Series[0].Points[0]
	if p.First != 1 || p.Last != 2 || !p.FirstObservedAt.Before(*p.LastObservedAt) {
		t.Fatal("history lost timestamp precision", p)
	}
	if p.MinimumObservedAt == nil || !p.MinimumObservedAt.Equal(*p.FirstObservedAt) || p.MaximumObservedAt == nil || !p.MaximumObservedAt.Equal(*p.LastObservedAt) || p.MaxGapSeconds == nil || *p.MaxGapSeconds < 800e-9 || *p.MaxGapSeconds >= .001 {
		t.Fatal("submillisecond continuity metadata understated gaps or lost extrema", p)
	}
}

func TestSignalHistoryContinuityRemainsBoundedAndSeparatesSparseBuckets(t *testing.T) {
	s, id := signalFixture(t)
	start := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	batch := SignalBatch{Source: "pi", BatchID: "continuous-and-gaps"}
	for i := range 120 {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprintf("dense-%03d", i), float64(i%40), start.Add(time.Duration(i)*10*time.Second)))
	}
	batch.Observations = append(batch.Observations, signalSample("after-outage", 90, start.Add(90*time.Minute)), signalSample("second-window", 10, start.Add(2*time.Hour)))
	ingestFixture(t, s, id, batch)
	for _, tc := range []struct {
		points  int
		wantGap float64
	}{{1, 4210}, {3, 10}} {
		history, err := s.SignalHistory(context.Background(), id, SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", From: start, To: start.Add(3 * time.Hour), MaxPoints: tc.points})
		if err != nil || len(history.Series) != 1 || len(history.Series[0].Points) > tc.points {
			t.Fatalf("bounded history: %+v %v", history, err)
		}
		p := history.Series[0].Points[0]
		if p.MaxGapSeconds == nil || *p.MaxGapSeconds != tc.wantGap {
			t.Fatalf("maxPoints=%d gap=%v, want %v", tc.points, p.MaxGapSeconds, tc.wantGap)
		}
		if tc.points == 3 {
			for _, singleton := range history.Series[0].Points[1:] {
				if singleton.MaxGapSeconds == nil || *singleton.MaxGapSeconds != 0 || singleton.Count != 1 {
					t.Fatal("singleton inherited another bucket's coverage", singleton)
				}
			}
		}
	}
}
