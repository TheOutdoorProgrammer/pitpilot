package garage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type StoredSignal struct {
	VehicleID   string            `json:"vehicleId"`
	Source      string            `json:"source"`
	Observation SignalObservation `json:"observation"`
}
type StoredSignalContext struct {
	VehicleID string        `json:"vehicleId,omitempty"`
	Source    string        `json:"source"`
	Context   SignalContext `json:"context"`
}
type StoredSignalBatch struct {
	VehicleID string `json:"vehicleId"`
	Source    string `json:"source"`
	BatchID   string `json:"batchId"`
	Hash      string `json:"hash"`
}

func initializeSignals(db *sql.DB) error {
	_, err := db.Exec(`BEGIN;
 CREATE TABLE IF NOT EXISTS signals(vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, source TEXT NOT NULL, key TEXT NOT NULL, metric TEXT NOT NULL, statistic TEXT NOT NULL, quality TEXT NOT NULL, sort_time INTEGER NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), PRIMARY KEY(vehicle_id,source,key));
 CREATE INDEX IF NOT EXISTS signals_history ON signals(vehicle_id,metric,statistic,sort_time);
 CREATE TABLE IF NOT EXISTS signal_contexts(vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, source TEXT NOT NULL, key TEXT NOT NULL, kind TEXT NOT NULL, sort_time INTEGER NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), PRIMARY KEY(vehicle_id,source,key));
 CREATE TABLE IF NOT EXISTS signal_batches(vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, source TEXT NOT NULL, batch_id TEXT NOT NULL, hash TEXT NOT NULL, PRIMARY KEY(vehicle_id,source,batch_id));
 COMMIT;`)
	return err
}

func normalizeSignalTime(t *time.Time) {
	if t != nil {
		*t = t.UTC()
	}
}
func normalizeSignalBatch(batch SignalBatch) (SignalBatch, error) {
	// Clone before sorting and normalizing so callers retain their original input.
	raw, err := json.Marshal(batch)
	if err != nil {
		return SignalBatch{}, err
	}
	var b SignalBatch
	if err = json.Unmarshal(raw, &b); err != nil {
		return SignalBatch{}, err
	}
	for i := range b.Observations {
		o := &b.Observations[i]
		for _, t := range []*time.Time{o.ObservedAt, o.PeriodStart, o.PeriodEnd, o.SourceRevision} {
			normalizeSignalTime(t)
		}
	}
	for i := range b.Contexts {
		c := &b.Contexts[i]
		for _, t := range []*time.Time{c.ObservedAt, c.PeriodStart, c.PeriodEnd} {
			normalizeSignalTime(t)
		}
		if c.Coverage != nil {
			normalizeSignalTime(c.Coverage.FirstObservedAt)
			normalizeSignalTime(c.Coverage.LastObservedAt)
		}
		if c.Segment != nil {
			c.Segment.StartedAt = c.Segment.StartedAt.UTC()
			c.Segment.EndedAt = c.Segment.EndedAt.UTC()
		}
	}
	sort.Slice(b.Observations, func(i, j int) bool { return b.Observations[i].Key < b.Observations[j].Key })
	sort.Slice(b.Contexts, func(i, j int) bool { return b.Contexts[i].Key < b.Contexts[j].Key })
	return b, nil
}

// Calendar dates use a sortable index only. This index is never an observation timestamp.
func signalSortTime(observed, end *time.Time, date string) int64 {
	if observed != nil {
		return observed.UnixMilli()
	}
	if end != nil {
		return end.UnixMilli()
	}
	t, _ := time.Parse("2006-01-02", date)
	return t.UnixMilli()
}
func sameSignalIdentity(a, b SignalObservation) bool {
	a.Value = 0
	b.Value = 0
	a.SampleCount = nil
	b.SampleCount = nil
	a.SourceRevision = nil
	b.SourceRevision = nil
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func (s *Store) IngestSignals(ctx context.Context, vehicleID string, batch SignalBatch) (report SignalIngestReport, err error) {
	ctx, done := operation(ctx, "db.signals.ingest")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	report, err = ingestSignalsTx(ctx, tx, vehicleID, batch)
	if err != nil {
		return SignalIngestReport{}, err
	}
	err = tx.Commit()
	return
}

func ingestSignalsTx(ctx context.Context, tx *sql.Tx, vehicleID string, batch SignalBatch) (report SignalIngestReport, err error) {
	if err = batch.Validate(); err != nil {
		return report, err
	}
	batch, err = normalizeSignalBatch(batch)
	if err != nil {
		return report, err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM vehicles WHERE id=?", vehicleID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return report, ErrNotFound
	} else if err != nil {
		return report, err
	}
	if batch.Source == "lubelogger" {
		if err = requireLegacySignalsAllowed(ctx, tx, vehicleID); err != nil {
			return report, err
		}
	}
	payload, _ := json.Marshal(batch)
	hash := digest(payload)
	var oldHash string
	err = tx.QueryRowContext(ctx, "SELECT hash FROM signal_batches WHERE vehicle_id=? AND source=? AND batch_id=?", vehicleID, batch.Source, batch.BatchID).Scan(&oldHash)
	if err == nil && oldHash != hash {
		return report, ErrSignalConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return report, err
	}
	for _, o := range batch.Observations {
		raw, _ := json.Marshal(o)
		var previous []byte
		err = tx.QueryRowContext(ctx, "SELECT data FROM signals WHERE vehicle_id=? AND source=? AND key=?", vehicleID, batch.Source, o.Key).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}
		if err == nil {
			if bytes.Equal(previous, raw) {
				report.Skipped++
				continue
			}
			var old SignalObservation
			if err = json.Unmarshal(previous, &old); err != nil {
				return report, err
			}
			if !sameSignalIdentity(old, o) || old.SourceRevision == nil || o.SourceRevision == nil {
				return report, ErrSignalConflict
			}
			if o.SourceRevision.Before(*old.SourceRevision) {
				report.Skipped++
				continue
			}
			if !o.SourceRevision.After(*old.SourceRevision) {
				return report, ErrSignalConflict
			}
			report.Updated++
		} else {
			report.Created++
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO signals(vehicle_id,source,key,metric,statistic,quality,sort_time,data) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(vehicle_id,source,key) DO UPDATE SET data=excluded.data`, vehicleID, batch.Source, o.Key, o.Metric, o.Statistic, o.Quality, signalSortTime(o.ObservedAt, o.PeriodEnd, o.CalendarDate), string(raw))
		if err != nil {
			return report, err
		}
	}
	for _, c := range batch.Contexts {
		if batch.Source == "pi" && isGPS(c) {
			excluded, e := gpsExcluded(ctx, tx, vehicleID, c)
			if e != nil {
				return report, e
			}
			if excluded {
				report.ContextsSkipped++
				continue
			}
		}
		raw, _ := json.Marshal(c)
		var previous []byte
		err = tx.QueryRowContext(ctx, "SELECT data FROM signal_contexts WHERE vehicle_id=? AND source=? AND key=?", vehicleID, batch.Source, c.Key).Scan(&previous)
		if err == nil {
			if !bytes.Equal(previous, raw) {
				return report, ErrSignalConflict
			}
			report.ContextsSkipped++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO signal_contexts(vehicle_id,source,key,kind,sort_time,data) VALUES(?,?,?,?,?,?)", vehicleID, batch.Source, c.Key, c.Kind, signalSortTime(c.ObservedAt, c.PeriodEnd, c.CalendarDate), string(raw))
		if err != nil {
			return report, err
		}
		report.ContextsCreated++
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO signal_batches(vehicle_id,source,batch_id,hash) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", vehicleID, batch.Source, batch.BatchID, hash)
	if err == nil && report.ContextsCreated > 0 {
		err = rebuildGPSRecordings(ctx, tx, vehicleID, batch)
	}
	return
}

func exportSignals(ctx context.Context, tx *sql.Tx, out *Export) error {
	out.Signals = []StoredSignal{}
	out.SignalContexts = []StoredSignalContext{}
	out.SignalBatches = []StoredSignalBatch{}
	rows, err := tx.QueryContext(ctx, "SELECT vehicle_id,source,data FROM signals ORDER BY vehicle_id,source,key")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v StoredSignal
		var raw []byte
		if err = rows.Scan(&v.VehicleID, &v.Source, &raw); err == nil {
			err = json.Unmarshal(raw, &v.Observation)
		}
		if err != nil {
			rows.Close()
			return err
		}
		out.Signals = append(out.Signals, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT vehicle_id,source,data FROM signal_contexts ORDER BY vehicle_id,source,key")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v StoredSignalContext
		var raw []byte
		if err = rows.Scan(&v.VehicleID, &v.Source, &raw); err == nil {
			err = json.Unmarshal(raw, &v.Context)
		}
		if err != nil {
			rows.Close()
			return err
		}
		out.SignalContexts = append(out.SignalContexts, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT vehicle_id,source,batch_id,hash FROM signal_batches ORDER BY vehicle_id,source,batch_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v StoredSignalBatch
		if err = rows.Scan(&v.VehicleID, &v.Source, &v.BatchID, &v.Hash); err != nil {
			return err
		}
		out.SignalBatches = append(out.SignalBatches, v)
	}
	return rows.Err()
}
