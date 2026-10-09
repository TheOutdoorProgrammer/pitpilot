package garage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// OdometerBaseline separates a known reading from the time after which captured
// distance may be added. AcceptedAt is never presented as a measurement time.
type OdometerBaseline struct {
	VehicleID    string     `json:"vehicleId"`
	Key          string     `json:"key"`
	Miles        float64    `json:"miles"`
	Status       string     `json:"status"`
	CalendarDate string     `json:"calendarDate"`
	RecordedAt   *time.Time `json:"recordedAt,omitempty"`
	AcceptedAt   time.Time  `json:"acceptedAt"`
}

func initializeOdometers(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS odometer_baselines(vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, key TEXT NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), PRIMARY KEY(vehicle_id,key))`); err != nil {
		return err
	}
	if err = seedOdometers(context.Background(), tx); err != nil {
		return err
	}
	if _, err = tx.Exec("PRAGMA user_version=5"); err != nil {
		return err
	}
	return tx.Commit()
}

func putOdometerBaseline(ctx context.Context, tx *sql.Tx, b OdometerBaseline) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO odometer_baselines(vehicle_id,key,data) VALUES(?,?,?) ON CONFLICT(vehicle_id,key) DO UPDATE SET data=excluded.data`, b.VehicleID, b.Key, string(raw))
	return err
}

func seedOdometers(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM vehicles WHERE id NOT IN (SELECT vehicle_id FROM odometer_baselines WHERE key='vehicle')`)
	if err != nil {
		return err
	}
	var vehicles []Vehicle
	for rows.Next() {
		var raw []byte
		var v Vehicle
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			rows.Close()
			return err
		}
		vehicles = append(vehicles, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, v := range vehicles {
		var date string
		// The imported dashboard is an absolute historical estimate, never a delta.
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(json_extract(data,'$.date')),'') FROM entries WHERE vehicle_id=? AND kind='record' AND json_extract(data,'$.odometerMiles')=? AND json_extract(data,'$.kind') NOT IN ('note','plan')`, v.ID, v.OdometerMiles).Scan(&date); err != nil {
			return err
		}
		if err = putOdometerBaseline(ctx, tx, OdometerBaseline{VehicleID: v.ID, Key: "vehicle", Miles: v.OdometerMiles, Status: v.OdometerStatus, CalendarDate: date, AcceptedAt: now}); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT data FROM entries WHERE kind='record' AND json_extract(data,'$.odometerStatus')='measured' AND NOT EXISTS (SELECT 1 FROM odometer_baselines b WHERE b.vehicle_id=entries.vehicle_id AND b.key=entries.id)`)
	if err != nil {
		return err
	}
	var records []Record
	for rows.Next() {
		var raw []byte
		var r Record
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			rows.Close()
			return err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range records {
		if err = syncRecordOdometer(ctx, tx, r); err != nil {
			return err
		}
	}
	return nil
}

func syncRecordOdometer(ctx context.Context, tx *sql.Tx, r Record) error {
	if r.Kind != "odometer" || r.OdometerStatus != "measured" {
		_, err := tx.ExecContext(ctx, "DELETE FROM odometer_baselines WHERE vehicle_id=? AND key=?", r.VehicleID, r.ID)
		return err
	}
	b := OdometerBaseline{VehicleID: r.VehicleID, Key: r.ID, Miles: r.OdometerMiles, Status: "measured", CalendarDate: r.Date, RecordedAt: r.RecordedAt, AcceptedAt: time.Now().UTC()}
	var previous []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM odometer_baselines WHERE vehicle_id=? AND key=?", r.VehicleID, r.ID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var old OdometerBaseline
		if err = json.Unmarshal(previous, &old); err != nil {
			return err
		}
		if old.Miles == b.Miles && old.CalendarDate == b.CalendarDate && equalOdometerTime(old.RecordedAt, b.RecordedAt) {
			return nil
		}
	}
	return putOdometerBaseline(ctx, tx, b)
}

func equalOdometerTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func odometerBaselineLater(a, b OdometerBaseline) bool {
	aOrder, bOrder := odometerOrderTime(a), odometerOrderTime(b)
	if !aOrder.Equal(bOrder) {
		return aOrder.After(bOrder)
	}
	if (a.Key == "vehicle") != (b.Key == "vehicle") {
		return b.Key == "vehicle"
	}
	return a.Key > b.Key
}

func odometerOrderTime(b OdometerBaseline) time.Time {
	if b.RecordedAt != nil {
		return b.RecordedAt.UTC()
	}
	day, err := time.Parse(time.DateOnly, b.CalendarDate)
	if err != nil {
		return time.Time{}
	}
	// Date-only records retain their supplied day. Clamp acceptance for ordering
	// only, so a later import/edit cannot promote a historical day into the present.
	first, last := day, day.Add(24*time.Hour-time.Nanosecond)
	if b.Key == "vehicle" || b.AcceptedAt.Before(first) {
		return first
	}
	if b.AcceptedAt.After(last) {
		return last
	}
	return b.AcceptedAt
}

type odometerQueries interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) projectOdometer(ctx context.Context, v *Vehicle) error {
	return projectOdometer(ctx, s.db, v)
}

func projectOdometer(ctx context.Context, q odometerQueries, v *Vehicle) (err error) {
	ctx, done := operation(ctx, "db.odometer.project")
	defer func() { done(err) }()
	rows, err := q.QueryContext(ctx, "SELECT data FROM odometer_baselines WHERE vehicle_id=?", v.ID)
	if err != nil {
		return err
	}
	var baseline *OdometerBaseline
	now := time.Now().UTC()
	for rows.Next() {
		var raw []byte
		var b OdometerBaseline
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &b); err != nil {
			rows.Close()
			return err
		}
		if b.RecordedAt != nil && b.RecordedAt.After(now) || b.CalendarDate > now.Add(14*time.Hour).Format(time.DateOnly) {
			continue
		}
		if baseline == nil || odometerBaselineLater(b, *baseline) {
			baseline = &b
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || baseline == nil {
		return err
	}
	v.OdometerMiles, v.OdometerStatus = baseline.Miles, baseline.Status
	v.OdometerExcludedIntervals = 0
	hasBaseline := baseline.Miles > 0 || baseline.Status == "measured" || baseline.Status == "estimated" || baseline.RecordedAt != nil
	cutoff := baseline.AcceptedAt
	if baseline.RecordedAt != nil {
		cutoff = *baseline.RecordedAt
	}
	// An OEM odometer is an absolute baseline. It replaces the previous baseline
	// only when its observation is newer, so cached OEM data cannot undo a correction.
	var smartcarRaw []byte
	err = q.QueryRowContext(ctx, `SELECT data FROM signals WHERE vehicle_id=? AND source='smartcar' AND metric='odometer_km' AND statistic='sample' AND quality='measured' AND sort_time<=? ORDER BY sort_time DESC,key LIMIT 1`, v.ID, now.UnixMilli()).Scan(&smartcarRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var o SignalObservation
		if err = json.Unmarshal(smartcarRaw, &o); err != nil {
			return err
		}
		baselineOrder := cutoff
		if baseline.Key == "vehicle" && baseline.RecordedAt == nil {
			baselineOrder = odometerOrderTime(*baseline)
		}
		if o.ObservedAt != nil && o.ObservedAt.After(baselineOrder) && !o.ObservedAt.After(now) {
			v.OdometerMiles, v.OdometerStatus = o.Value/1.609344, "measured"
			cutoff = *o.ObservedAt
			hasBaseline = true
		}
	}
	if !hasBaseline {
		return nil
	}
	rows, err = q.QueryContext(ctx, `SELECT data FROM signals WHERE vehicle_id=? AND source='pi' AND metric='driving_distance_km' AND statistic='sum' AND sort_time>=? ORDER BY sort_time,key`, v.ID, cutoff.UnixMilli())
	if err != nil {
		return err
	}
	var intervals []SignalObservation
	for rows.Next() {
		var raw []byte
		var o SignalObservation
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &o); err != nil {
			rows.Close()
			return err
		}
		if o.PeriodStart == nil || o.PeriodEnd == nil || o.PeriodStart.Before(cutoff) || o.PeriodEnd.After(now) {
			continue
		}
		intervals = append(intervals, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Slice(intervals, func(i, j int) bool {
		if !intervals[i].PeriodStart.Equal(*intervals[j].PeriodStart) {
			return intervals[i].PeriodStart.Before(*intervals[j].PeriodStart)
		}
		return intervals[i].Key < intervals[j].Key
	})
	// Exclude entire overlapping groups, so duplicate devices and late arrival
	// cannot make the result depend on delivery order or inflate distance.
	for i := 0; i < len(intervals); {
		end := *intervals[i].PeriodEnd
		j := i + 1
		for j < len(intervals) && intervals[j].PeriodStart.Before(end) {
			if intervals[j].PeriodEnd.After(end) {
				end = *intervals[j].PeriodEnd
			}
			j++
		}
		if j == i+1 && intervals[i].Value > 0 {
			v.OdometerMiles += intervals[i].Value / 1.609344
			v.OdometerStatus = "estimated"
		}
		if j > i+1 {
			v.OdometerExcludedIntervals += j - i
		}
		i = j
	}
	return nil
}

func exportOdometerBaselines(ctx context.Context, tx *sql.Tx) ([]OdometerBaseline, error) {
	rows, err := tx.QueryContext(ctx, "SELECT data FROM odometer_baselines ORDER BY vehicle_id,key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OdometerBaseline
	for rows.Next() {
		var raw []byte
		var b OdometerBaseline
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
