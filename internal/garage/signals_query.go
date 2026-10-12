package garage

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

type LatestSignalSeries struct {
	Metric    string            `json:"metric"`
	Unit      string            `json:"unit"`
	Source    string            `json:"source"`
	Statistic string            `json:"statistic"`
	Quality   string            `json:"quality"`
	Latest    SignalObservation `json:"latest"`
	Stale     bool              `json:"stale"`
}
type LatestSignals struct {
	HistoryRevision string                `json:"historyRevision,omitempty"`
	AsOf            time.Time             `json:"asOf"`
	Definitions     []SignalDefinition    `json:"definitions"`
	Series          []LatestSignalSeries  `json:"series"`
	Contexts        []StoredSignalContext `json:"contexts"`
}

func (s *Store) LatestSignals(ctx context.Context, vehicleID string) (out LatestSignals, err error) {
	ctx, done := operation(ctx, "db.signals.latest")
	defer func() { done(err) }()
	out = LatestSignals{AsOf: time.Now().UTC(), Definitions: SignalDefinitions(), Series: []LatestSignalSeries{}, Contexts: []StoredSignalContext{}}
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
	var discardedAt string
	if err = tx.QueryRowContext(ctx, "SELECT discarded_at FROM legacy_signal_policies WHERE vehicle_id=?", vehicleID).Scan(&discardedAt); err == nil {
		out.HistoryRevision = digest([]byte(discardedAt))
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT source,data FROM (SELECT source,data,metric,statistic,quality,DENSE_RANK() OVER(PARTITION BY source,metric,statistic,quality ORDER BY sort_time DESC) rank FROM signals WHERE vehicle_id=?) WHERE rank=1 ORDER BY metric,source,statistic,quality`, vehicleID)
	if err != nil {
		return out, err
	}
	seriesIndex := map[string]int{}
	for rows.Next() {
		var v LatestSignalSeries
		var raw []byte
		if err = rows.Scan(&v.Source, &raw); err == nil {
			err = json.Unmarshal(raw, &v.Latest)
		}
		if err != nil {
			rows.Close()
			return out, err
		}
		o := v.Latest
		v.Metric = o.Metric
		v.Unit = o.Unit
		v.Statistic = o.Statistic
		v.Quality = o.Quality
		v.Stale = o.ObservedAt == nil || o.ObservedAt.After(out.AsOf) || out.AsOf.Sub(*o.ObservedAt) > time.Duration(signalDefinitions[o.Metric].StaleAfterSeconds)*time.Second
		identity := v.Metric + "/" + v.Source + "/" + v.Statistic + "/" + v.Quality
		if i, ok := seriesIndex[identity]; ok {
			old := out.Series[i].Latest
			currentTime, oldTime := signalObservationTime(o), signalObservationTime(old)
			if currentTime.After(oldTime) || (currentTime.Equal(oldTime) && o.Key > old.Key) {
				out.Series[i] = v
			}
		} else {
			seriesIndex[identity] = len(out.Series)
			out.Series = append(out.Series, v)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT source,data FROM (SELECT source,data,kind,ROW_NUMBER() OVER(PARTITION BY source,kind,COALESCE(json_extract(data,'$.diagnostic.class'),'') ORDER BY sort_time DESC,key DESC) rank FROM signal_contexts WHERE vehicle_id=?) WHERE rank=1 ORDER BY source,kind`, vehicleID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v StoredSignalContext
		var raw []byte
		if err = rows.Scan(&v.Source, &raw); err == nil {
			err = json.Unmarshal(raw, &v.Context)
		}
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Contexts = append(out.Contexts, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

type SignalHistoryQuery struct {
	Metric, Statistic, Source, Quality string
	Binning                            string
	From, To                           time.Time
	MaxPoints                          int
}

func signalObservationTime(o SignalObservation) time.Time {
	if o.ObservedAt != nil {
		return *o.ObservedAt
	}
	if o.PeriodEnd != nil {
		return *o.PeriodEnd
	}
	value, _ := time.Parse("2006-01-02", o.CalendarDate)
	return value
}

func (q SignalHistoryQuery) Validate() error {
	if q.Binning != "" && q.Binning != "adaptive" && q.Binning != "time" {
		return errors.New("invalid history binning")
	}
	if _, ok := signalUnit(q.Metric, q.Statistic); !ok {
		return errors.New("unknown signal metric")
	}
	switch q.Statistic {
	case "all", "sample", "snapshot", "min", "max", "mean", "sum", "count":
	default:
		return errors.New("invalid signal statistic")
	}
	if !q.To.After(q.From) || q.To.Sub(q.From) > 366*24*time.Hour || q.MaxPoints < 1 || q.MaxPoints > 500 {
		return errors.New("history requires an ordered range of at most 366 days and 1 to 500 points")
	}
	if q.Source != "" && !validSignalSource(q.Source) {
		return errors.New("invalid signal source")
	}
	if q.Quality != "" && q.Quality != "measured" && q.Quality != "estimated" && q.Quality != "derived" {
		return errors.New("invalid signal quality")
	}
	return nil
}

type SignalHistoryPoint struct {
	// BucketIndex is unique only within a response series, not an observation ID.
	BucketIndex       *int       `json:"bucketIndex,omitempty"`
	BucketStart       *time.Time `json:"bucketStart,omitempty"`
	BucketEnd         *time.Time `json:"bucketEnd,omitempty"`
	WindowStart       *time.Time `json:"windowStart,omitempty"`
	WindowEnd         *time.Time `json:"windowEnd,omitempty"`
	Minimum           float64    `json:"minimum"`
	Maximum           float64    `json:"maximum"`
	Mean              float64    `json:"mean"`
	First             float64    `json:"first"`
	Last              float64    `json:"last"`
	Count             int        `json:"count"`
	FirstObservedAt   *time.Time `json:"firstObservedAt,omitempty"`
	LastObservedAt    *time.Time `json:"lastObservedAt,omitempty"`
	MinimumObservedAt *time.Time `json:"minimumObservedAt,omitempty"`
	MaximumObservedAt *time.Time `json:"maximumObservedAt,omitempty"`
	MaxGapSeconds     *float64   `json:"maxGapSeconds,omitempty"`
	CalendarDate      string     `json:"calendarDate,omitempty"`
	Timezone          string     `json:"timezone,omitempty"`
}
type SignalHistorySeries struct {
	Source    string               `json:"source"`
	Quality   string               `json:"quality"`
	Statistic string               `json:"statistic"`
	Unit      string               `json:"unit"`
	Points    []SignalHistoryPoint `json:"points"`
}
type SignalHistory struct {
	Metric    string                `json:"metric"`
	Unit      string                `json:"unit"`
	From      time.Time             `json:"from"`
	To        time.Time             `json:"to"`
	MaxPoints int                   `json:"maxPoints"`
	Series    []SignalHistorySeries `json:"series"`
}

func (s *Store) SignalHistory(ctx context.Context, vehicleID string, q SignalHistoryQuery) (out SignalHistory, err error) {
	ctx, done := operation(ctx, "db.signals.history")
	defer func() { done(err) }()
	if err = q.Validate(); err != nil {
		return out, err
	}
	unit, _ := signalUnit(q.Metric, q.Statistic)
	out = SignalHistory{Metric: q.Metric, Unit: unit, From: q.From.UTC(), To: q.To.UTC(), MaxPoints: q.MaxPoints, Series: []SignalHistorySeries{}}
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
	// Streaming keeps memory bounded by buckets, including on long, high-frequency drives.
	calendarFrom, _ := time.Parse("2006-01-02", q.From.UTC().Format("2006-01-02"))
	upper := q.To
	if q.Statistic != "sample" {
		upper = upper.Add(366 * 24 * time.Hour)
	}
	query := `SELECT source,quality,data FROM signals WHERE vehicle_id=? AND metric=? AND (?='all' OR statistic=?) AND sort_time>=? AND sort_time<=? AND (?='' OR source=?) AND (?='' OR quality=?) ORDER BY sort_time,key`
	args := []any{vehicleID, q.Metric, q.Statistic, q.Statistic, calendarFrom.UnixMilli(), upper.UnixMilli(), q.Source, q.Source, q.Quality, q.Quality}
	var plans map[string]*signalSamplePlan
	if q.Binning != "time" {
		plans, err = planSignalSamples(ctx, tx, query, args, q)
		if err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	groups := map[string]map[string]*SignalHistoryPoint{}
	identities := map[string]SignalHistorySeries{}
	width := q.To.Sub(q.From) / time.Duration(q.MaxPoints)
	if width < time.Millisecond {
		width = time.Millisecond
	}
	for rows.Next() {
		var source, quality string
		var raw []byte
		var o SignalObservation
		if err = rows.Scan(&source, &quality, &raw); err == nil {
			err = json.Unmarshal(raw, &o)
		}
		if err != nil {
			rows.Close()
			return out, err
		}
		if !signalInHistory(o, q) {
			continue
		}
		id := source + "/" + quality + "/" + o.Statistic
		bucket := "date/" + o.CalendarDate
		var start, end *time.Time
		var bucketIndex *int
		adaptive := q.Binning != "time" && o.Statistic == "sample" && o.ObservedAt != nil
		if adaptive {
			index := plans[id].nextBucket()
			bucketIndex = &index
			bucket = fmt.Sprintf("sample/%06d", index)
		} else if o.CalendarDate == "" {
			at := time.UnixMilli(signalSortTime(o.ObservedAt, o.PeriodEnd, ""))
			if q.Binning == "time" {
				at = signalObservationTime(o)
			}
			index := int(at.Sub(q.From) / width)
			if index < 0 {
				index = 0
			}
			if index >= q.MaxPoints {
				index = q.MaxPoints - 1
			}
			a := q.From.Add(time.Duration(index) * width).UTC()
			b := a.Add(width)
			if index == q.MaxPoints-1 || b.After(q.To) {
				b = q.To.UTC()
			}
			start = &a
			end = &b
			bucket = fmt.Sprintf("time/%06d", index)
		}
		if groups[id] == nil {
			groups[id] = map[string]*SignalHistoryPoint{}
			identities[id] = SignalHistorySeries{Source: source, Quality: quality, Statistic: o.Statistic, Unit: o.Unit, Points: []SignalHistoryPoint{}}
		}
		p := groups[id][bucket]
		if p == nil {
			p = &SignalHistoryPoint{BucketIndex: bucketIndex, BucketStart: start, BucketEnd: end, Minimum: o.Value, Maximum: o.Value, First: o.Value, CalendarDate: o.CalendarDate, Timezone: o.Timezone}
			if o.ObservedAt != nil {
				p.MinimumObservedAt, p.MaximumObservedAt = o.ObservedAt, o.ObservedAt
				p.MaxGapSeconds = new(float64)
			}
			groups[id][bucket] = p
		}
		accumulateSignalHistory(p, o, adaptive)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		series := identities[id]
		keys := make([]string, 0, len(groups[id]))
		for key := range groups[id] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			series.Points = append(series.Points, *groups[id][key])
		}
		out.Series = append(out.Series, series)
	}
	return out, tx.Commit()
}

func accumulateSignalHistory(p *SignalHistoryPoint, o SignalObservation, adaptive bool) {
	if o.Value < p.Minimum || (o.Value == p.Minimum && o.ObservedAt != nil && p.MinimumObservedAt != nil && o.ObservedAt.Before(*p.MinimumObservedAt)) {
		p.Minimum = o.Value
		p.MinimumObservedAt = o.ObservedAt
	}
	if o.Value > p.Maximum || (o.Value == p.Maximum && o.ObservedAt != nil && p.MaximumObservedAt != nil && o.ObservedAt.Before(*p.MaximumObservedAt)) {
		p.Maximum = o.Value
		p.MaximumObservedAt = o.ObservedAt
	}
	p.Count++
	p.Mean += (o.Value - p.Mean) / float64(p.Count)
	if o.ObservedAt == nil {
		p.Last = o.Value
	}
	if o.ObservedAt != nil {
		// The index orders milliseconds. Ties may arrive out of nanosecond order,
		// so this high-water gap conservatively overstates a gap by at most 1 ms.
		if p.LastObservedAt != nil {
			gap := o.ObservedAt.Sub(*p.LastObservedAt).Seconds()
			if gap < 0 {
				gap = -gap
			}
			if gap > *p.MaxGapSeconds {
				*p.MaxGapSeconds = gap
			}
		}
		if p.FirstObservedAt == nil || o.ObservedAt.Before(*p.FirstObservedAt) {
			p.FirstObservedAt = o.ObservedAt
			p.First = o.Value
		}
		if p.LastObservedAt == nil || !o.ObservedAt.Before(*p.LastObservedAt) {
			p.LastObservedAt = o.ObservedAt
			p.Last = o.Value
		}
		if p.WindowStart == nil || o.ObservedAt.Before(*p.WindowStart) {
			p.WindowStart = o.ObservedAt
		}
		if p.WindowEnd == nil || o.ObservedAt.After(*p.WindowEnd) {
			p.WindowEnd = o.ObservedAt
		}
		if adaptive {
			p.BucketStart, p.BucketEnd = p.FirstObservedAt, p.LastObservedAt
		}
	} else if o.PeriodStart != nil {
		if p.WindowStart == nil || o.PeriodStart.Before(*p.WindowStart) {
			p.WindowStart = o.PeriodStart
		}
		if p.WindowEnd == nil || o.PeriodEnd.After(*p.WindowEnd) {
			p.WindowEnd = o.PeriodEnd
		}
	}
}

func signalInHistory(o SignalObservation, q SignalHistoryQuery) bool {
	if o.ObservedAt != nil {
		return !o.ObservedAt.Before(q.From) && o.ObservedAt.Before(q.To)
	}
	if o.PeriodStart != nil {
		return o.PeriodStart.Before(q.To) && o.PeriodEnd.After(q.From)
	}
	date, _ := time.Parse("2006-01-02", o.CalendarDate)
	calendarFrom, _ := time.Parse("2006-01-02", q.From.UTC().Format("2006-01-02"))
	return !date.Before(calendarFrom) && date.Before(q.To.UTC())
}

type signalSampleGap struct {
	index int
	gap   time.Duration
}

// Retaining only the largest gaps bounds memory even when every reading is a
// separate session. Equal gaps retain earlier boundaries deterministically.
type signalSampleGaps []signalSampleGap

func (g signalSampleGaps) Len() int { return len(g) }
func (g signalSampleGaps) Less(i, j int) bool {
	if g[i].gap == g[j].gap {
		return g[i].index > g[j].index
	}
	return g[i].gap < g[j].gap
}
func (g signalSampleGaps) Swap(i, j int) { g[i], g[j] = g[j], g[i] }
func (g *signalSampleGaps) Push(x any)   { *g = append(*g, x.(signalSampleGap)) }
func (g *signalSampleGaps) Pop() any {
	last := (*g)[len(*g)-1]
	*g = (*g)[:len(*g)-1]
	return last
}

type signalSampleSegment struct {
	start, count, buckets, offset, remainder int
}

type signalSamplePlan struct {
	count, seen, segment int
	last                 time.Time
	gaps                 signalSampleGaps
	segments             []signalSampleSegment
}

func (p *signalSamplePlan) observe(at time.Time, budget int) {
	gap := at.Sub(p.last)
	if p.count > 0 && gap > 15*time.Minute && budget > 1 {
		boundary := signalSampleGap{index: p.count, gap: gap}
		if len(p.gaps) < budget-1 {
			heap.Push(&p.gaps, boundary)
		} else if gap > p.gaps[0].gap {
			p.gaps[0] = boundary
			heap.Fix(&p.gaps, 0)
		}
	}
	p.count++
	if at.After(p.last) {
		p.last = at
	}
}

func (p *signalSamplePlan) allocate(budget int) {
	sort.Slice(p.gaps, func(i, j int) bool { return p.gaps[i].index < p.gaps[j].index })
	start := 0
	for _, gap := range p.gaps {
		p.segments = append(p.segments, signalSampleSegment{start: start, count: gap.index - start, buckets: 1})
		start = gap.index
	}
	p.segments = append(p.segments, signalSampleSegment{start: start, count: p.count - start, buckets: 1})
	remaining := min(budget, p.count) - len(p.segments)
	capacity := p.count - len(p.segments)
	allocated := 0
	for i := range p.segments {
		s := &p.segments[i]
		if capacity > 0 {
			s.buckets += remaining * (s.count - 1) / capacity
			s.remainder = remaining * (s.count - 1) % capacity
		}
		allocated += s.buckets - 1
	}
	order := make([]int, len(p.segments))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return p.segments[order[i]].remainder > p.segments[order[j]].remainder })
	for _, i := range order[:remaining-allocated] {
		p.segments[i].buckets++
	}
	offset := 0
	for i := range p.segments {
		p.segments[i].offset = offset
		offset += p.segments[i].buckets
	}
	p.gaps = nil
}

func (p *signalSamplePlan) nextBucket() int {
	if p.seen >= p.segments[p.segment].start+p.segments[p.segment].count {
		p.segment++
	}
	s := p.segments[p.segment]
	bucket := s.offset + (p.seen-s.start)*s.buckets/s.count
	p.seen++
	return bucket
}

func planSignalSamples(ctx context.Context, tx *sql.Tx, query string, args []any, q SignalHistoryQuery) (map[string]*signalSamplePlan, error) {
	// Spend the point budget on recorded samples, not empty requested time. Both
	// passes share a read transaction and filter so concurrent ingestion cannot
	// change segment counts between planning and reduction.
	plans := map[string]*signalSamplePlan{}
	if q.Statistic != "all" && q.Statistic != "sample" {
		return plans, nil
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, quality string
		var raw []byte
		var o SignalObservation
		if err := rows.Scan(&source, &quality, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		if o.Statistic != "sample" || o.ObservedAt == nil || !signalInHistory(o, q) {
			continue
		}
		id := source + "/" + quality + "/" + o.Statistic
		if plans[id] == nil {
			plans[id] = &signalSamplePlan{}
		}
		plans[id].observe(*o.ObservedAt, q.MaxPoints)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, plan := range plans {
		plan.allocate(q.MaxPoints)
	}
	return plans, nil
}
