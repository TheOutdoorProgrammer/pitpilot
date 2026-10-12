package garage

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSignalPreviewSessionBoundsAndHistoryEquivalence(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	batch := SignalBatch{Source: "pi", BatchID: "sessions"}
	batch.Observations = append(batch.Observations, signalSample("old", 999, at.Add(-time.Hour)))
	for i := 0; i < 100; i++ {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprintf("new-%03d", i), float64(i%11), at.Add(time.Duration(i)*time.Second)))
	}
	ingestFixture(t, s, id, batch)
	other := signalSample("older-source", 500, at.Add(-time.Minute))
	ingestFixture(t, s, id, SignalBatch{Source: "smartcar", BatchID: "other", Observations: []SignalObservation{other}})
	before, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.SignalPreviews(context.Background(), id, SignalPreviewQuery{Metrics: []string{"manifold_kpa", "rpm"}, MaxPoints: 10})
	if err != nil {
		t.Fatal(err)
	}
	p := out.Previews[0]
	if p.Scope != "latest_session" || p.Truncated || p.Selection != "latest_series" || len(p.History.Series) != 1 || len(p.History.Series[0].Points) > 10 || p.History.Series[0].Source != "pi" || !p.History.From.Equal(at) {
		t.Fatalf("incorrect session preview: %+v", p)
	}
	if out.Previews[1].Scope != "empty" || len(out.Previews[1].History.Series) != 0 {
		t.Fatal("invented empty history")
	}
	h, err := s.SignalHistory(context.Background(), id, SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", Source: "pi", Quality: "measured", From: p.History.From, To: p.History.To, MaxPoints: 10, Binning: "time"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for i, point := range p.History.Series[0].Points {
		point.BucketIndex = nil
		if !reflect.DeepEqual(point, h.Series[0].Points[i]) {
			t.Fatalf("preview/detail mismatch at bucket%d: %+v vs %+v", i, point, h.Series[0].Points[i])
		}
		count += point.Count
	}
	if count != 100 {
		t.Fatalf("lost session readings: %d", count)
	}
	after, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Signals, after.Signals) {
		t.Fatal("preview rewrote samples")
	}
}

func TestSignalPreviewHardReadLimitAndIndexPlan(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	// Equal times must not force an unbounded sort before LIMIT.
	for batchN := 0; batchN < 8; batchN++ {
		batch := SignalBatch{Source: "pi", BatchID: fmt.Sprintf("batch%d", batchN)}
		for i := 0; i < 100; i++ {
			batch.Observations = append(batch.Observations, signalSample(fmt.Sprintf("p-%04d", batchN*100+i), float64(i), at))
		}
		ingestFixture(t, s, id, batch)
	}
	q := SignalPreviewQuery{Metrics: []string{"manifold_kpa"}, MaxPoints: 1}
	out, err := s.SignalPreviews(context.Background(), id, q)
	if err != nil {
		t.Fatal(err)
	}
	p := out.Previews[0]
	if !p.Truncated || len(p.History.Series[0].Points) != 1 || p.History.Series[0].Points[0].Count != signalPreviewReadLimit {
		t.Fatalf("unbounded preview: %+v", p)
	}
	copy, err := s.SignalPreviews(context.Background(), id, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, copy.Previews[0]) {
		t.Fatal("same-timestamp selection changed")
	}
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+signalPreviewSQL, id, "manifold_kpa", "sample", time.Now().UnixMilli(), 513)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN signals") {
			t.Fatal("unbounded preview query plan", detail)
		}
		indexed = indexed || strings.Contains(detail, "signals_history")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("preview did not use history index")
	}
}

func TestSignalPreviewSlowTrendAndDiscreteSemantics(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for _, metric := range []string{"fuel_level_pct", "mil_on", "fuel_system_1_status", "dtc_count"} {
		batch := SignalBatch{Source: "pi", BatchID: metric}
		for i := 0; i < 3; i++ {
			when := at.Add(time.Duration(i) * time.Second)
			if metric == "fuel_level_pct" {
				when = at.Add(-time.Duration(40-i*20) * 24 * time.Hour)
			}
			value := float64(i % 2)
			if metric == "fuel_system_1_status" {
				value = float64(1 + i)
			}
			batch.Observations = append(batch.Observations, SignalObservation{Key: fmt.Sprintf("%s%d", metric, i), Metric: metric, Unit: signalDefinitions[metric].Unit, Statistic: "sample", Quality: "measured", Value: value, ObservedAt: &when})
		}
		ingestFixture(t, s, id, batch)
	}
	out, err := s.SignalPreviews(context.Background(), id, SignalPreviewQuery{Metrics: []string{"fuel_level_pct", "mil_on", "fuel_system_1_status", "dtc_count"}, MaxPoints: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.Previews[0].Scope != "recent_trend" || out.Previews[0].History.Series[0].Points[0].Count != 2 {
		t.Fatal("slow trend crossed thirty days")
	}
	for _, p := range out.Previews[1:] {
		point := p.History.Series[0].Points[0]
		if p.Aggregation != "last" || point.Minimum == point.Maximum || point.Count != 3 || math.Trunc(point.Last) != point.Last {
			t.Fatalf("invalid discrete preview %+v", p)
		}
	}
}

func TestSignalHistoryTimeBinningPreservesSamplesAndAdaptiveDefault(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	batch := SignalBatch{Source: "pi", BatchID: "spikes"}
	for i := 0; i < 30; i++ {
		batch.Observations = append(batch.Observations, signalSample(fmt.Sprint(i), float64(i%3)*20, at.Add(time.Duration(i)*time.Second)))
	}
	ingestFixture(t, s, id, batch)
	q := SignalHistoryQuery{Metric: "manifold_kpa", Statistic: "sample", From: at, To: at.Add(24 * time.Hour), MaxPoints: 10}
	adaptive, err := s.SignalHistory(context.Background(), id, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Binning = "adaptive"
	explicit, err := s.SignalHistory(context.Background(), id, q)
	if err != nil || !reflect.DeepEqual(adaptive, explicit) {
		t.Fatal("default adaptive contract changed", err)
	}
	q.Binning = "time"
	fixed, err := s.SignalHistory(context.Background(), id, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(adaptive.Series[0].Points) != 10 || len(fixed.Series[0].Points) != 1 {
		t.Fatal("fixed intervals still replay adaptive trip density")
	}
	p := fixed.Series[0].Points[0]
	if p.Count != 30 || p.Mean != 20 || p.Minimum != 0 || p.Maximum != 40 || !p.MaximumObservedAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("lost raw summary evidence: %+v", p)
	}
}
