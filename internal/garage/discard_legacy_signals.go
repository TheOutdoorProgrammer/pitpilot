package garage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
)

var ErrLegacyDiscardConflict = errors.New("legacy history changed or ownership could not be verified; preview again")

type LegacySignalDiscardReport struct {
	PreviewToken              string `json:"previewToken"`
	Applied                   bool   `json:"applied"`
	VehiclesAffected          int    `json:"vehiclesAffected"`
	PoliciesCreated           int    `json:"policiesCreated"`
	PoliciesExisting          int    `json:"policiesExisting"`
	LubeLoggerSamplesDeleted  int    `json:"lubeLoggerSamplesDeleted"`
	LubeLoggerContextsDeleted int    `json:"lubeLoggerContextsDeleted"`
	ReceiverSamplesDeleted    int    `json:"receiverSamplesDeleted"`
	ReceiverContextsDeleted   int    `json:"receiverContextsDeleted"`
	ReceiverArchivesDeleted   int    `json:"receiverArchivesDeleted"`
	ReusedSamplesPreserved    int    `json:"reusedSamplesPreserved"`
	ReusedContextsPreserved   int    `json:"reusedContextsPreserved"`
}

// Hash the complete ownership state, including rows that must survive. A native
// upload between preview and apply invalidates the token instead of being hidden
// behind an old count or an archive-only hash.
func hashLegacyDiscardState(ctx context.Context, tx *sql.Tx, h hash.Hash) error {
	for _, table := range []struct{ name, order string }{
		{"vehicles", "id"}, {"signals", "vehicle_id,source,key"}, {"signal_contexts", "vehicle_id,source,key"}, {"signal_batches", "vehicle_id,source,batch_id"},
		{"receiver_event_archives", "device_id,event_id"}, {"converted_signal_notes", "note_id"}, {"converted_signal_note_versions", "note_id,version"},
		{"import_sources", "source,collection,source_id"}, {"import_settings", "source"}, {"legacy_signal_policies", "vehicle_id"},
	} {
		receiverHash(h, table.name)
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+table.name+" ORDER BY "+table.order)
		if err != nil {
			return err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		receiverHash(h, columns)
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err = rows.Scan(destinations...); err != nil {
				break
			}
			receiverHash(h, values)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type legacyDiscardKey struct{ vehicle, key string }
type legacyDiscardPlan struct {
	samples        map[legacyDiscardKey]bool
	contexts       map[legacyDiscardKey]bool
	reusedSamples  map[legacyDiscardKey]bool
	reusedContexts map[legacyDiscardKey]bool
	archives       int
}

func requireReceiverObservation(ctx context.Context, tx *sql.Tx, vehicle string, o SignalObservation) error {
	var metric, statistic, quality string
	var index int64
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT metric,statistic,quality,sort_time,data FROM signals WHERE vehicle_id=? AND source='pi' AND key=?", vehicle, o.Key).Scan(&metric, &statistic, &quality, &index, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLegacyDiscardConflict
	}
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(o)
	if metric != o.Metric || statistic != o.Statistic || quality != o.Quality || index != signalSortTime(o.ObservedAt, o.PeriodEnd, o.CalendarDate) || !bytes.Equal(raw, expected) {
		return ErrLegacyDiscardConflict
	}
	return nil
}
func requireReceiverContext(ctx context.Context, tx *sql.Tx, vehicle string, c SignalContext) error {
	var kind string
	var index int64
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT kind,sort_time,data FROM signal_contexts WHERE vehicle_id=? AND source='pi' AND key=?", vehicle, c.Key).Scan(&kind, &index, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLegacyDiscardConflict
	}
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(c)
	if kind != c.Kind || index != signalSortTime(c.ObservedAt, c.PeriodEnd, c.CalendarDate) || !bytes.Equal(raw, expected) {
		return ErrLegacyDiscardConflict
	}
	return nil
}

func planReceiverDiscard(ctx context.Context, tx *sql.Tx) (legacyDiscardPlan, error) {
	plan := legacyDiscardPlan{samples: map[legacyDiscardKey]bool{}, contexts: map[legacyDiscardKey]bool{}, reusedSamples: map[legacyDiscardKey]bool{}, reusedContexts: map[legacyDiscardKey]bool{}}
	archives, err := exportReceiverArchives(ctx, tx)
	if err != nil {
		return plan, err
	}
	for _, archive := range archives {
		event, err := receiverhistory.Parse(archive.Raw)
		digestBytes, digestErr := hex.DecodeString(archive.BackupSHA256)
		if err != nil || digestErr != nil || len(digestBytes) != 32 || event.DeviceID != archive.DeviceID || event.ID != archive.EventID {
			return plan, ErrLegacyDiscardConflict
		}
		var boot, sequence string
		if err = tx.QueryRowContext(ctx, "SELECT boot_id,sequence FROM receiver_event_archives WHERE device_id=? AND event_id=?", archive.DeviceID, archive.EventID).Scan(&boot, &sequence); err != nil {
			return plan, err
		}
		if boot != event.BootID || sequence != fmt.Sprint(event.Sequence) {
			return plan, ErrLegacyDiscardConflict
		}
		id := "receiver:" + digest([]byte(event.DeviceID+":"+event.ID))
		expected := SignalBatch{Source: "pi", BatchID: id}
		if event.ObservedAt != nil {
			expected, err = MeasuredOBDBatch(event.Readings, event.DTCs, event.PendingDTCs, event.PermanentDTCs, *event.ObservedAt, id)
			if err != nil {
				return plan, ErrLegacyDiscardConflict
			}
			expected, err = normalizeSignalBatch(expected)
			if err != nil {
				return plan, err
			}
		}
		projection := archive.Projection
		if projection.Source != expected.Source || projection.BatchID != expected.BatchID || len(projection.Observations) != len(expected.Observations) || len(projection.Contexts) != len(expected.Contexts) {
			return plan, ErrLegacyDiscardConflict
		}
		owned := SignalBatch{Source: "pi", BatchID: id}
		for i, o := range projection.Observations {
			if !receiverEqualObservation(o, expected.Observations[i]) {
				return plan, ErrLegacyDiscardConflict
			}
			if err = requireReceiverObservation(ctx, tx, archive.VehicleID, o); err != nil {
				return plan, err
			}
			key := legacyDiscardKey{archive.VehicleID, o.Key}
			if o.Key == expected.Observations[i].Key {
				plan.samples[key] = true
				owned.Observations = append(owned.Observations, o)
			} else {
				plan.reusedSamples[key] = true
			}
		}
		for i, c := range projection.Contexts {
			if !receiverEqualContext(c, expected.Contexts[i]) {
				return plan, ErrLegacyDiscardConflict
			}
			if err = requireReceiverContext(ctx, tx, archive.VehicleID, c); err != nil {
				return plan, err
			}
			key := legacyDiscardKey{archive.VehicleID, c.Key}
			if c.Key == expected.Contexts[i].Key {
				plan.contexts[key] = true
				owned.Contexts = append(owned.Contexts, c)
			} else {
				plan.reusedContexts[key] = true
			}
		}
		if len(owned.Observations)+len(owned.Contexts) > 0 {
			owned, err = normalizeSignalBatch(owned)
			if err != nil {
				return plan, err
			}
			raw, _ := json.Marshal(owned)
			var marker string
			err = tx.QueryRowContext(ctx, "SELECT hash FROM signal_batches WHERE vehicle_id=? AND source='pi' AND batch_id=?", archive.VehicleID, id).Scan(&marker)
			if errors.Is(err, sql.ErrNoRows) {
				return plan, ErrLegacyDiscardConflict
			}
			if err != nil {
				return plan, err
			}
			if marker != digest(raw) {
				return plan, ErrLegacyDiscardConflict
			}
		} else {
			var marker string
			err = tx.QueryRowContext(ctx, "SELECT hash FROM signal_batches WHERE vehicle_id=? AND source='pi' AND batch_id=?", archive.VehicleID, id).Scan(&marker)
			if err == nil {
				return plan, ErrLegacyDiscardConflict
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return plan, err
			}
		}
		plan.archives++
	}
	// Two retained events can legitimately share a recovered measurement. Its
	// original owner still proves deletion; a dangling receiver namespace does not.
	for _, pair := range []struct{ owned, reused map[legacyDiscardKey]bool }{{plan.samples, plan.reusedSamples}, {plan.contexts, plan.reusedContexts}} {
		for key := range pair.reused {
			if pair.owned[key] {
				delete(pair.reused, key)
			} else if strings.HasPrefix(key.key, "receiver:") {
				return plan, ErrLegacyDiscardConflict
			}
		}
	}
	return plan, nil
}

func deleteReceiverKeys(ctx context.Context, tx *sql.Tx, table string, keys map[legacyDiscardKey]bool) error {
	ordered := make([]legacyDiscardKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].vehicle != ordered[j].vehicle {
			return ordered[i].vehicle < ordered[j].vehicle
		}
		return ordered[i].key < ordered[j].key
	})
	for _, key := range ordered {
		result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE vehicle_id=? AND source='pi' AND key=?", key.vehicle, key.key)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrLegacyDiscardConflict
		}
	}
	return nil
}

func (s *Store) DiscardLegacySignals(ctx context.Context, token string) (report LegacySignalDiscardReport, err error) {
	ctx, done := operation(ctx, "db.legacy_signals.discard")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	h := sha256.New()
	receiverHash(h, "discard-legacy-signals-v1")
	if err = hashLegacyDiscardState(ctx, tx, h); err != nil {
		return report, err
	}
	report.PreviewToken = hex.EncodeToString(h.Sum(nil))
	if token != "" && token != report.PreviewToken {
		return report, ErrLegacyDiscardConflict
	}
	plan, err := planReceiverDiscard(ctx, tx)
	if err != nil {
		return report, err
	}
	report.ReceiverSamplesDeleted = len(plan.samples)
	report.ReceiverContextsDeleted = len(plan.contexts)
	report.ReceiverArchivesDeleted = plan.archives
	report.ReusedSamplesPreserved = len(plan.reusedSamples)
	report.ReusedContextsPreserved = len(plan.reusedContexts)
	rows, err := tx.QueryContext(ctx, `SELECT vehicle_id FROM signals WHERE source='lubelogger'
 UNION SELECT vehicle_id FROM signal_contexts WHERE source='lubelogger'
 UNION SELECT vehicle_id FROM receiver_event_archives
 UNION SELECT vehicle_id FROM converted_signal_notes WHERE json_extract(data,'$.batch.source')='lubelogger' ORDER BY vehicle_id`)
	if err != nil {
		return report, err
	}
	vehicles := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		vehicles = append(vehicles, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return report, err
	}
	report.VehiclesAffected = len(vehicles)
	for _, table := range []struct {
		name  string
		count *int
	}{{"signals", &report.LubeLoggerSamplesDeleted}, {"signal_contexts", &report.LubeLoggerContextsDeleted}} {
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table.name+" WHERE source='lubelogger'").Scan(table.count); err != nil {
			return report, err
		}
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	for _, vehicle := range vehicles {
		result, e := tx.ExecContext(ctx, "INSERT INTO legacy_signal_policies(vehicle_id,discarded_at) VALUES(?,?) ON CONFLICT DO NOTHING", vehicle, at)
		if e != nil {
			return report, e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return report, e
		}
		if count == 1 {
			report.PoliciesCreated++
		} else {
			report.PoliciesExisting++
		}
	}
	if err = deleteReceiverKeys(ctx, tx, "signals", plan.samples); err != nil {
		return report, err
	}
	if err = deleteReceiverKeys(ctx, tx, "signal_contexts", plan.contexts); err != nil {
		return report, err
	}
	for _, table := range []string{"signals", "signal_contexts"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE source='lubelogger'"); err != nil {
			return report, err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM receiver_event_archives"); err != nil {
		return report, err
	}
	if token != "" {
		if err = tx.Commit(); err != nil {
			return report, err
		}
		report.Applied = true
	}
	return report, nil
}
