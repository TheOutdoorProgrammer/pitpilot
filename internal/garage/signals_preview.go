package garage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const signalPreviewReadLimit = 512

type SignalPreviewQuery struct {
	Metrics   []string
	MaxPoints int
}

func (q SignalPreviewQuery) Validate() error {
	if len(q.Metrics) < 1 || len(q.Metrics) > 32 || q.MaxPoints < 1 || q.MaxPoints > 10 {
		return errors.New("previews require 1 to 32 metrics and 1 to 10 points")
	}
	seen := map[string]bool{}
	for _, metric := range q.Metrics {
		if _, ok := signalDefinitions[metric]; !ok || seen[metric] {
			return errors.New("previews require unique known metrics")
		}
		seen[metric] = true
	}
	return nil
}

type SignalPreview struct {
	Metric      string        `json:"metric"`
	Scope       string        `json:"scope"`
	Aggregation string        `json:"aggregation"`
	Selection   string        `json:"selection"`
	Truncated   bool          `json:"truncated"`
	History     SignalHistory `json:"history"`
}

type SignalPreviews struct {
	AsOf     time.Time       `json:"asOf"`
	Previews []SignalPreview `json:"previews"`
}

type previewReading struct {
	source      string
	observation SignalObservation
}

// Ordering by the index's implicit rowid tie-breaker avoids sorting an
// unbounded equal-timestamp group before LIMIT can take effect.
const signalPreviewSQL = `SELECT source,data FROM signals WHERE vehicle_id=? AND metric=? AND statistic=? AND sort_time<=? ORDER BY sort_time DESC,rowid DESC LIMIT ?`

func (s *Store) SignalPreviews(ctx context.Context, vehicleID string, q SignalPreviewQuery) (out SignalPreviews, err error) {
	ctx, done := operation(ctx, "db.signals.previews")
	defer func() { done(err) }()
	if err = q.Validate(); err != nil {
		return out, err
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("preview.metric_count", len(q.Metrics)), attribute.Int("preview.point_limit", q.MaxPoints))
	out = SignalPreviews{AsOf: time.Now().UTC(), Previews: []SignalPreview{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM vehicles WHERE id=?", vehicleID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	for _, metric := range q.Metrics {
		readings := make([]previewReading, 0, 2*(signalPreviewReadLimit+1))
		clipped := map[string]bool{}
		for _, statistic := range []string{"sample", "snapshot"} {
			rows, e := tx.QueryContext(ctx, signalPreviewSQL, vehicleID, metric, statistic, out.AsOf.UnixMilli(), signalPreviewReadLimit+1)
			if e != nil {
				return out, e
			}
			n := 0
			for rows.Next() {
				var v previewReading
				var raw []byte
				if err = rows.Scan(&v.source, &raw); err == nil {
					err = json.Unmarshal(raw, &v.observation)
				}
				if err != nil {
					rows.Close()
					return out, err
				}
				n++
				if v.observation.ObservedAt != nil && !v.observation.ObservedAt.After(out.AsOf) {
					readings = append(readings, v)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
			clipped[statistic] = n > signalPreviewReadLimit
		}
		out.Previews = append(out.Previews, makeSignalPreview(metric, readings, clipped, q.MaxPoints, out.AsOf))
	}
	return out, tx.Commit()
}

func slowPreviewMetric(metric, source string) bool {
	if source == "smartcar" || strings.HasPrefix(metric, "tire_") {
		return true
	}
	switch metric {
	case "fuel_level_pct", "fuel_remaining_l", "oil_life_pct", "odometer_km", "range_km":
		return true
	}
	return false
}

func makeSignalPreview(metric string, readings []previewReading, clipped map[string]bool, budget int, now time.Time) SignalPreview {
	unit := signalDefinitions[metric].Unit
	out := SignalPreview{Metric: metric, Scope: "empty", Aggregation: "sample_mean", Selection: "latest_series", History: SignalHistory{Metric: metric, Unit: unit, From: now, To: now, MaxPoints: budget, Series: []SignalHistorySeries{}}}
	if unit == "boolean" || unit == "code" || unit == "count" {
		out.Aggregation = "last"
	}
	if len(readings) == 0 {
		return out
	}
	sort.Slice(readings, func(i, j int) bool {
		a, b := readings[i], readings[j]
		if !a.observation.ObservedAt.Equal(*b.observation.ObservedAt) {
			return a.observation.ObservedAt.After(*b.observation.ObservedAt)
		}
		if a.observation.Key != b.observation.Key {
			return a.observation.Key > b.observation.Key
		}
		return a.source > b.source
	})
	latest := readings[0]
	out.Scope = "latest_session"
	slow := slowPreviewMetric(metric, latest.source)
	if slow {
		out.Scope = "recent_trend"
	}
	selected := make([]SignalObservation, 0, signalPreviewReadLimit)
	previous := *latest.observation.ObservedAt
	cutoff := previous.Add(-30 * 24 * time.Hour)
	out.Truncated = clipped[latest.observation.Statistic]
	for _, v := range readings {
		o := v.observation
		if v.source != latest.source || o.Quality != latest.observation.Quality || o.Statistic != latest.observation.Statistic {
			continue
		}
		if (slow && o.ObservedAt.Before(cutoff)) || (!slow && previous.Sub(*o.ObservedAt) > 15*time.Minute) {
			out.Truncated = false
			break
		}
		if len(selected) == signalPreviewReadLimit {
			out.Truncated = true
			break
		}
		selected = append(selected, o)
		previous = *o.ObservedAt
	}
	// Ascending capture order keeps shared extrema, gap and endpoint semantics
	// identical to detailed history, without loading its unbounded raw window.
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	out.History.From = *selected[0].ObservedAt
	// Native Date precision cannot reliably retain a one-nanosecond exclusive
	// bound. Match the history index's millisecond resolution instead.
	out.History.To = latest.observation.ObservedAt.Add(time.Millisecond)
	width := out.History.To.Sub(out.History.From) / time.Duration(budget)
	if width < time.Millisecond {
		width = time.Millisecond
	}
	points := make([]*SignalHistoryPoint, budget)
	for _, o := range selected {
		index := min(int(o.ObservedAt.Sub(out.History.From)/width), budget-1)
		p := points[index]
		if p == nil {
			start := out.History.From.Add(time.Duration(index) * width)
			end := start.Add(width)
			if index == budget-1 || end.After(out.History.To) {
				end = out.History.To
			}
			p = &SignalHistoryPoint{BucketIndex: &index, BucketStart: &start, BucketEnd: &end, Minimum: o.Value, Maximum: o.Value, First: o.Value, MinimumObservedAt: o.ObservedAt, MaximumObservedAt: o.ObservedAt, MaxGapSeconds: new(float64)}
			points[index] = p
		}
		accumulateSignalHistory(p, o, false)
	}
	series := SignalHistorySeries{Source: latest.source, Quality: latest.observation.Quality, Statistic: latest.observation.Statistic, Unit: latest.observation.Unit, Points: []SignalHistoryPoint{}}
	for _, p := range points {
		if p != nil {
			series.Points = append(series.Points, *p)
		}
	}
	out.History.Series = append(out.History.Series, series)
	return out
}
