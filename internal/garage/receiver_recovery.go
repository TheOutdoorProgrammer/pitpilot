package garage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
)

var ErrReceiverConflict = errors.New("receiver recovery conflicts with retained history; nothing was imported")

type ReceiverRecoveryRequest struct {
	VehicleID    string                   `json:"vehicleId"`
	Snapshot     receiverhistory.Snapshot `json:"snapshot"`
	PreviewToken string                   `json:"previewToken,omitempty"`
}
type ReceiverRecoveryReport struct {
	PreviewToken    string `json:"previewToken"`
	Applied         bool   `json:"applied"`
	Events          int    `json:"events"`
	Duplicates      int    `json:"duplicates"`
	Archived        int    `json:"archived"`
	Skipped         int    `json:"skipped"`
	UnknownClock    int    `json:"unknownClock"`
	SamplesCreated  int    `json:"samplesCreated"`
	SamplesReused   int    `json:"samplesReused"`
	ContextsCreated int    `json:"contextsCreated"`
	ContextsReused  int    `json:"contextsReused"`
}
type ReceiverEventArchive struct {
	VehicleID    string          `json:"vehicleId"`
	DeviceID     string          `json:"deviceId"`
	EventID      string          `json:"eventId"`
	BackupSHA256 string          `json:"backupSha256"`
	Raw          json.RawMessage `json:"raw"`
	Projection   SignalBatch     `json:"projection"`
}

func initializeReceiverRecovery(db *sql.DB) error {
	_, err := db.Exec(`BEGIN; CREATE TABLE IF NOT EXISTS receiver_event_archives (
 device_id TEXT NOT NULL,event_id TEXT NOT NULL,vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
 boot_id TEXT NOT NULL,sequence TEXT NOT NULL,backup_sha256 TEXT NOT NULL,raw TEXT NOT NULL CHECK(json_valid(raw)),projection TEXT NOT NULL CHECK(json_valid(projection)),
 PRIMARY KEY(device_id,event_id),UNIQUE(device_id,boot_id,sequence)); PRAGMA user_version=7; COMMIT;`)
	return err
}

type receiverInput struct {
	event     receiverhistory.Event
	raw       json.RawMessage
	canonical string
}

func prepareReceiverInput(request ReceiverRecoveryRequest) ([]receiverInput, int, error) {
	s := request.Snapshot
	hashBytes, err := hex.DecodeString(s.SHA256)
	if err != nil || len(hashBytes) != 32 || s.SHA256 != hex.EncodeToString(hashBytes) || !receiverhistory.Identifier.MatchString(s.DeviceID) || request.VehicleID == "" || len(request.VehicleID) > 128 || len(s.Events) == 0 || len(s.Events) > receiverhistory.MaxEvents {
		return nil, 0, receiverhistory.ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > receiverhistory.MaxRequestBytes {
		return nil, 0, receiverhistory.ErrInvalid
	}
	inputs := map[string]receiverInput{}
	sequences := map[string]string{}
	duplicates := 0
	for _, raw := range s.Events {
		e, err := receiverhistory.Parse(raw)
		if err != nil || e.DeviceID != s.DeviceID {
			return nil, 0, receiverhistory.ErrInvalid
		}
		canonicalRaw, err := canonical(raw)
		if err != nil {
			return nil, 0, receiverhistory.ErrInvalid
		}
		canonicalString := string(canonicalRaw)
		if old, ok := inputs[e.ID]; ok {
			if old.canonical != canonicalString {
				return nil, 0, ErrReceiverConflict
			}
			duplicates++
			continue
		}
		seq := fmt.Sprintf("%s:%d", e.BootID, e.Sequence)
		if _, ok := sequences[seq]; ok {
			return nil, 0, ErrReceiverConflict
		}
		sequences[seq] = e.ID
		// Validate the common projection even for unknown clocks, without assigning
		// the validation clock to the archived event or to any stored observation.
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if e.ObservedAt != nil {
			at = *e.ObservedAt
		}
		if _, err = MeasuredOBDBatch(e.Readings, e.DTCs, e.PendingDTCs, e.PermanentDTCs, at, "receiver:"+digest([]byte(s.DeviceID+":"+e.ID))); err != nil {
			return nil, 0, receiverhistory.ErrInvalid
		}
		inputs[e.ID] = receiverInput{e, append(json.RawMessage(nil), raw...), canonicalString}
	}
	result := make([]receiverInput, 0, len(inputs))
	for _, input := range inputs {
		result = append(result, input)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].event.ID < result[j].event.ID })
	return result, duplicates, nil
}

type receiverState struct {
	observations     map[string]SignalObservation
	contexts         map[string]SignalContext
	observationTimes map[string][]string
	contextTimes     map[string][]string
	archives         map[string]ReceiverEventArchive
	sequences        map[string]string
}

func receiverObservationIdentity(o SignalObservation) string {
	if o.ObservedAt == nil || o.Statistic != "sample" {
		return ""
	}
	return o.ObservedAt.UTC().Format(time.RFC3339Nano) + ":" + o.Metric
}
func receiverContextIdentity(c SignalContext) string {
	if c.ObservedAt == nil || c.Diagnostic == nil {
		return ""
	}
	return c.ObservedAt.UTC().Format(time.RFC3339Nano) + ":" + c.Diagnostic.Class
}
func receiverEqualObservation(a, b SignalObservation) bool {
	a.Key = ""
	b.Key = ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func receiverEqualContext(a, b SignalContext) bool {
	a.Key = ""
	b.Key = ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func receiverHash(h hash.Hash, v any) { raw, _ := json.Marshal(v); h.Write(raw); h.Write([]byte{0}) }

func loadReceiverState(ctx context.Context, tx *sql.Tx, vehicleID, deviceID string, h hash.Hash) (receiverState, error) {
	state := receiverState{map[string]SignalObservation{}, map[string]SignalContext{}, map[string][]string{}, map[string][]string{}, map[string]ReceiverEventArchive{}, map[string]string{}}
	var vehicle string
	if err := tx.QueryRowContext(ctx, "SELECT data FROM vehicles WHERE id=?", vehicleID).Scan(&vehicle); errors.Is(err, sql.ErrNoRows) {
		return state, ErrNotFound
	} else if err != nil {
		return state, err
	}
	receiverHash(h, vehicle)
	for _, table := range []string{"signals", "signal_contexts"} {
		rows, err := tx.QueryContext(ctx, "SELECT key,data FROM "+table+" WHERE vehicle_id=? AND source='pi' ORDER BY key", vehicleID)
		if err != nil {
			return state, err
		}
		count := 0
		for rows.Next() {
			var key string
			var raw []byte
			if err = rows.Scan(&key, &raw); err != nil {
				break
			}
			count++
			if count > 1000000 {
				err = receiverhistory.ErrInvalid
				break
			}
			receiverHash(h, []string{table, key, string(raw)})
			if table == "signals" {
				var o SignalObservation
				if err = json.Unmarshal(raw, &o); err != nil {
					break
				}
				if o.Key != key {
					err = ErrReceiverConflict
					break
				}
				state.observations[key] = o
				if identity := receiverObservationIdentity(o); identity != "" {
					state.observationTimes[identity] = append(state.observationTimes[identity], key)
				}
			} else {
				var c SignalContext
				if err = json.Unmarshal(raw, &c); err != nil {
					break
				}
				if c.Key != key {
					err = ErrReceiverConflict
					break
				}
				state.contexts[key] = c
				if identity := receiverContextIdentity(c); identity != "" {
					state.contextTimes[identity] = append(state.contextTimes[identity], key)
				}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return state, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT event_id,vehicle_id,boot_id,sequence,backup_sha256,raw,projection FROM receiver_event_archives WHERE device_id=? ORDER BY event_id", deviceID)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var a ReceiverEventArchive
		var boot, seq string
		var raw, projection []byte
		a.DeviceID = deviceID
		if err = rows.Scan(&a.EventID, &a.VehicleID, &boot, &seq, &a.BackupSHA256, &raw, &projection); err != nil {
			return state, err
		}
		a.Raw = raw
		if a.VehicleID != vehicleID {
			return state, ErrReceiverConflict
		}
		if err = json.Unmarshal(projection, &a.Projection); err != nil {
			return state, err
		}
		state.archives[a.EventID] = a
		state.sequences[boot+":"+seq] = a.EventID
		receiverHash(h, a)
	}
	return state, rows.Err()
}

func (s *Store) RecoverReceiver(ctx context.Context, request ReceiverRecoveryRequest) (report ReceiverRecoveryReport, err error) {
	ctx, done := operation(ctx, "db.receiver_recovery.reconcile")
	defer func() { done(err) }()
	inputs, duplicates, err := prepareReceiverInput(request)
	if err != nil {
		return report, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	h := sha256.New()
	receiverHash(h, []string{"receiver-recovery-v1", request.VehicleID, request.Snapshot.DeviceID, request.Snapshot.SHA256})
	for _, input := range inputs {
		receiverHash(h, input.canonical)
	}
	receiverHash(h, duplicates)
	state, err := loadReceiverState(ctx, tx, request.VehicleID, request.Snapshot.DeviceID, h)
	if err != nil {
		return report, err
	}
	report.PreviewToken = hex.EncodeToString(h.Sum(nil))
	report.Events = len(inputs)
	report.Duplicates = duplicates
	if request.PreviewToken != "" && request.PreviewToken != report.PreviewToken {
		return report, ErrReceiverConflict
	}
	for _, input := range inputs {
		e := input.event
		id := "receiver:" + digest([]byte(e.DeviceID+":"+e.ID))
		projection := SignalBatch{Source: "pi", BatchID: id}
		if e.ObservedAt == nil {
			report.UnknownClock++
		} else {
			projection, err = MeasuredOBDBatch(e.Readings, e.DTCs, e.PendingDTCs, e.PermanentDTCs, *e.ObservedAt, id)
			if err != nil {
				return report, receiverhistory.ErrInvalid
			}
			projection, err = normalizeSignalBatch(projection)
			if err != nil {
				return report, err
			}
		}
		if archived, ok := state.archives[e.ID]; ok {
			if state.sequences[fmt.Sprintf("%s:%d", e.BootID, e.Sequence)] != e.ID {
				return report, ErrReceiverConflict
			}
			previous, canonicalErr := canonical(archived.Raw)
			if canonicalErr != nil || string(previous) != input.canonical {
				return report, ErrReceiverConflict
			}
			if archived.Projection.Source != projection.Source || archived.Projection.BatchID != projection.BatchID || len(archived.Projection.Observations) != len(projection.Observations) || len(archived.Projection.Contexts) != len(projection.Contexts) {
				return report, ErrReceiverConflict
			}
			for i, o := range archived.Projection.Observations {
				existing, ok := state.observations[o.Key]
				if !ok || !receiverEqualObservation(o, existing) || !receiverEqualObservation(o, projection.Observations[i]) {
					return report, ErrReceiverConflict
				}
			}
			for i, c := range archived.Projection.Contexts {
				existing, ok := state.contexts[c.Key]
				if !ok || !receiverEqualContext(c, existing) || !receiverEqualContext(c, projection.Contexts[i]) {
					return report, ErrReceiverConflict
				}
			}
			report.Skipped++
			continue
		}
		seq := fmt.Sprintf("%s:%d", e.BootID, e.Sequence)
		if _, ok := state.sequences[seq]; ok {
			return report, ErrReceiverConflict
		}
		state.sequences[seq] = e.ID
		missing := SignalBatch{Source: "pi", BatchID: id}
		if e.ObservedAt != nil {
			for i, o := range projection.Observations {
				identity := receiverObservationIdentity(o)
				keys := state.observationTimes[identity]
				if len(keys) > 0 {
					for _, key := range keys {
						if !receiverEqualObservation(o, state.observations[key]) {
							return report, ErrReceiverConflict
						}
					}
					projection.Observations[i].Key = keys[0]
					report.SamplesReused++
				} else {
					if _, exists := state.observations[o.Key]; exists {
						return report, ErrReceiverConflict
					}
					missing.Observations = append(missing.Observations, o)
					state.observations[o.Key] = o
					state.observationTimes[identity] = []string{o.Key}
					report.SamplesCreated++
				}
			}
			for i, c := range projection.Contexts {
				identity := receiverContextIdentity(c)
				keys := state.contextTimes[identity]
				if len(keys) > 0 {
					for _, key := range keys {
						if !receiverEqualContext(c, state.contexts[key]) {
							return report, ErrReceiverConflict
						}
					}
					projection.Contexts[i].Key = keys[0]
					report.ContextsReused++
				} else {
					if _, exists := state.contexts[c.Key]; exists {
						return report, ErrReceiverConflict
					}
					missing.Contexts = append(missing.Contexts, c)
					state.contexts[c.Key] = c
					state.contextTimes[identity] = []string{c.Key}
					report.ContextsCreated++
				}
			}
			if len(missing.Observations)+len(missing.Contexts) > 0 {
				if _, err = ingestSignalsTx(ctx, tx, request.VehicleID, missing); err != nil {
					return report, err
				}
			}
		}
		encoded, _ := json.Marshal(projection)
		if _, err = tx.ExecContext(ctx, "INSERT INTO receiver_event_archives(device_id,event_id,vehicle_id,boot_id,sequence,backup_sha256,raw,projection) VALUES(?,?,?,?,?,?,?,?)", e.DeviceID, e.ID, request.VehicleID, e.BootID, fmt.Sprint(e.Sequence), request.Snapshot.SHA256, string(input.raw), string(encoded)); err != nil {
			return report, err
		}
		report.Archived++
	}
	if request.PreviewToken != "" {
		if err = tx.Commit(); err != nil {
			return report, err
		}
		report.Applied = true
	}
	return report, nil
}

func exportReceiverArchives(ctx context.Context, tx *sql.Tx) ([]ReceiverEventArchive, error) {
	result := []ReceiverEventArchive{}
	rows, err := tx.QueryContext(ctx, "SELECT vehicle_id,device_id,event_id,backup_sha256,raw,projection FROM receiver_event_archives ORDER BY device_id,event_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a ReceiverEventArchive
		var raw, projection []byte
		if err = rows.Scan(&a.VehicleID, &a.DeviceID, &a.EventID, &a.BackupSHA256, &raw, &projection); err != nil {
			return nil, err
		}
		a.Raw = raw
		if err = json.Unmarshal(projection, &a.Projection); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
