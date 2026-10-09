package garage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

func Open(filename string) (*Store, error) {
	if filename != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = file.Close(); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;`); err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 5 {
		db.Close()
		return nil, errors.New("database schema is newer than this server")
	}
	if version == 0 {
		_, err = db.Exec(`BEGIN;
		CREATE TABLE vehicles(id TEXT PRIMARY KEY, data TEXT NOT NULL CHECK(json_valid(data)));
		CREATE TABLE entries(id TEXT PRIMARY KEY, vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, kind TEXT NOT NULL CHECK(kind IN ('record','reminder','trip')), data TEXT NOT NULL CHECK(json_valid(data)));
		CREATE INDEX entries_vehicle_kind ON entries(vehicle_id,kind);
		PRAGMA user_version=1;
		COMMIT;`)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	if version < 2 {
		_, err = db.Exec(`BEGIN;
		CREATE TABLE import_sources(source TEXT NOT NULL, collection TEXT NOT NULL, source_id TEXT NOT NULL, target_id TEXT NOT NULL UNIQUE, target_kind TEXT NOT NULL, source_hash TEXT NOT NULL, target_hash TEXT NOT NULL, raw TEXT NOT NULL CHECK(json_valid(raw)), PRIMARY KEY(source,collection,source_id));
		PRAGMA user_version=2;
		COMMIT;`)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	// Early schema 2 rehearsal databases contained only the source mapping table.
	// Settings are pinned when an import is first applied.
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS import_settings(source TEXT PRIMARY KEY, settings TEXT NOT NULL CHECK(json_valid(settings)));`); err != nil {
		db.Close()
		return nil, err
	}
	if err = initializeSignals(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = initializeSignalConversions(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = initializeDevices(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = initializeSmartcar(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = initializeOdometers(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("entropy unavailable")
	}
	return hex.EncodeToString(b[:])
}

func operation(ctx context.Context, name string) (context.Context, func(error)) {
	ctx, span := otel.Tracer("pitpilot/store").Start(ctx, name)
	return ctx, func(err error) {
		if err != nil && !errors.Is(err, ErrNotFound) {
			span.SetStatus(codes.Error, "database operation failed")
		}
		span.End()
	}
}

func (s *Store) Vehicles(ctx context.Context) (out []Vehicle, err error) {
	ctx, done := operation(ctx, "db.vehicles.list")
	defer func() { done(err) }()
	out = []Vehicle{}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM vehicles ORDER BY json_extract(data,'$.createdAt'),id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var v Vehicle
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err = s.projectOdometer(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) Vehicle(ctx context.Context, id string) (v Vehicle, err error) {
	ctx, done := operation(ctx, "db.vehicle.get")
	defer func() { done(err) }()
	var data []byte
	if err = s.db.QueryRowContext(ctx, "SELECT data FROM vehicles WHERE id=?", id).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(data, &v)
	if err == nil {
		err = s.projectOdometer(ctx, &v)
	}
	return v, err
}

func (s *Store) CreateVehicle(ctx context.Context, v Vehicle) (err error) {
	ctx, done := operation(ctx, "db.vehicle.save")
	defer func() { done(err) }()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO vehicles(id,data) VALUES(?,?)", v.ID, string(data)); err != nil {
		return err
	}
	if err = seedOdometers(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateVehicle(ctx context.Context, id string, recalibrate bool, change func(*Vehicle) error) (v Vehicle, err error) {
	ctx, done := operation(ctx, "db.vehicle.update")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	var data []byte
	if err = tx.QueryRowContext(ctx, "SELECT data FROM vehicles WHERE id=?", id).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	previousMiles, previousStatus := v.OdometerMiles, v.OdometerStatus
	if recalibrate {
		if err = projectOdometer(ctx, tx, &v); err != nil {
			return v, err
		}
		v.OdometerExcludedIntervals = 0
	}
	if err = change(&v); err != nil {
		return v, err
	}
	data, err = json.Marshal(v)
	if err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE vehicles SET data=? WHERE id=?", string(data), id); err != nil {
		return v, err
	}
	if recalibrate || v.OdometerMiles != previousMiles || v.OdometerStatus != previousStatus {
		now := time.Now().UTC()
		if err = putOdometerBaseline(ctx, tx, OdometerBaseline{VehicleID: v.ID, Key: "vehicle", Miles: v.OdometerMiles, Status: v.OdometerStatus, CalendarDate: now.Format(time.DateOnly), RecordedAt: &now, AcceptedAt: now}); err != nil {
			return v, err
		}
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	err = s.projectOdometer(ctx, &v)
	return v, err
}

func (s *Store) DeleteVehicle(ctx context.Context, id string) (err error) {
	ctx, done := operation(ctx, "db.vehicle.delete")
	defer func() { done(err) }()
	result, err := s.db.ExecContext(ctx, "DELETE FROM vehicles WHERE id=?", id)
	return affected(result, err)
}

func (s *Store) Entries(ctx context.Context, vehicleID, kind string) (out []json.RawMessage, err error) {
	ctx, done := operation(ctx, "db.entries.list")
	defer func() { done(err) }()
	out = []json.RawMessage{}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM entries WHERE vehicle_id=? AND kind=? ORDER BY id", vehicleID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(data))
	}
	return out, rows.Err()
}

func (s *Store) Entry(ctx context.Context, id, kind string) (out json.RawMessage, err error) {
	ctx, done := operation(ctx, "db.entry.get")
	defer func() { done(err) }()
	var data []byte
	err = s.db.QueryRowContext(ctx, "SELECT data FROM entries WHERE id=? AND kind=?", id, kind).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return json.RawMessage(data), err
}

func (s *Store) SaveEntry(ctx context.Context, id, vehicleID, kind string, v any, create bool) (err error) {
	ctx, done := operation(ctx, "db.entry.save")
	defer func() { done(err) }()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if create {
		_, err = tx.ExecContext(ctx, "INSERT INTO entries(id,vehicle_id,kind,data) VALUES(?,?,?,?)", id, vehicleID, kind, string(data))
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE entries SET data=? WHERE id=? AND vehicle_id=? AND kind=?", string(data), id, vehicleID, kind)
		err = affected(result, err)
	}
	if err != nil {
		return err
	}
	if kind == "record" {
		var record Record
		if err = json.Unmarshal(data, &record); err != nil {
			return err
		}
		if err = syncRecordOdometer(ctx, tx, record); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteEntry(ctx context.Context, id, kind string) (err error) {
	ctx, done := operation(ctx, "db.entry.delete")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM entries WHERE id=? AND kind=?", id, kind)
	if err = affected(result, err); err != nil {
		return err
	}
	if kind == "record" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM odometer_baselines WHERE key=?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func affected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

type Export struct {
	SchemaVersion        int                   `json:"schemaVersion"`
	ExportedAt           time.Time             `json:"exportedAt"`
	Vehicles             []json.RawMessage     `json:"vehicles"`
	Records              []json.RawMessage     `json:"records"`
	Reminders            []json.RawMessage     `json:"reminders"`
	Trips                []json.RawMessage     `json:"trips"`
	ImportSources        []ImportedSource      `json:"importSources"`
	ImportSettings       []ImportSettings      `json:"importSettings"`
	Signals              []StoredSignal        `json:"signals"`
	SignalContexts       []StoredSignalContext `json:"signalContexts"`
	SignalBatches        []StoredSignalBatch   `json:"signalBatches"`
	ConvertedSignalNotes []ConvertedSignalNote `json:"convertedSignalNotes"`
	OdometerBaselines    []OdometerBaseline    `json:"odometerBaselines,omitempty"`
}

type ImportSettings struct {
	Source   string          `json:"source"`
	Settings json.RawMessage `json:"settings"`
}

type ImportedSource struct {
	Source     string          `json:"source"`
	Collection string          `json:"collection"`
	SourceID   string          `json:"sourceId"`
	TargetID   string          `json:"targetId"`
	TargetKind string          `json:"targetKind"`
	SourceHash string          `json:"sourceHash"`
	TargetHash string          `json:"targetHash"`
	Raw        json.RawMessage `json:"raw"`
}

func (s *Store) Export(ctx context.Context) (out Export, err error) {
	ctx, done := operation(ctx, "db.export")
	defer func() { done(err) }()
	out = Export{SchemaVersion: 3, ExportedAt: time.Now().UTC(), Vehicles: []json.RawMessage{}, Records: []json.RawMessage{}, Reminders: []json.RawMessage{}, Trips: []json.RawMessage{}, ImportSources: []ImportedSource{}, ImportSettings: []ImportSettings{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT 'vehicle',data FROM vehicles UNION ALL SELECT kind,data FROM entries")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var kind string
		var data []byte
		if err = rows.Scan(&kind, &data); err != nil {
			rows.Close()
			return out, err
		}
		switch kind {
		case "vehicle":
			out.Vehicles = append(out.Vehicles, json.RawMessage(data))
		case "record":
			out.Records = append(out.Records, json.RawMessage(data))
		case "reminder":
			out.Reminders = append(out.Reminders, json.RawMessage(data))
		case "trip":
			out.Trips = append(out.Trips, json.RawMessage(data))
		default:
			rows.Close()
			return out, fmt.Errorf("invalid entry kind")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	imports, err := tx.QueryContext(ctx, "SELECT source,collection,source_id,target_id,target_kind,source_hash,target_hash,raw FROM import_sources ORDER BY source,collection,source_id")
	if err != nil {
		return out, err
	}
	for imports.Next() {
		var v ImportedSource
		var raw []byte
		if err = imports.Scan(&v.Source, &v.Collection, &v.SourceID, &v.TargetID, &v.TargetKind, &v.SourceHash, &v.TargetHash, &raw); err != nil {
			imports.Close()
			return out, err
		}
		v.Raw = json.RawMessage(raw)
		out.ImportSources = append(out.ImportSources, v)
	}
	err = imports.Err()
	imports.Close()
	if err != nil {
		return out, err
	}
	settings, err := tx.QueryContext(ctx, "SELECT source,settings FROM import_settings ORDER BY source")
	if err != nil {
		return out, err
	}
	for settings.Next() {
		var v ImportSettings
		var raw []byte
		if err = settings.Scan(&v.Source, &raw); err != nil {
			settings.Close()
			return out, err
		}
		v.Settings = json.RawMessage(raw)
		out.ImportSettings = append(out.ImportSettings, v)
	}
	err = settings.Err()
	settings.Close()
	if err != nil {
		return out, err
	}
	if err = exportSignals(ctx, tx, &out); err != nil {
		return out, err
	}
	if out.ConvertedSignalNotes, err = exportSignalConversions(ctx, tx); err != nil {
		return out, err
	}
	if out.OdometerBaselines, err = exportOdometerBaselines(ctx, tx); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
