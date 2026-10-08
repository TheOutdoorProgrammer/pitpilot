package garage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
)

var ErrImportConflict = errors.New("migration has conflicts or its preview is stale")
var ErrImportSettingsChanged = errors.New("source interpretation settings differ from the existing import")

// ImportItem keeps the source document independent of its editable projection.
type ImportItem struct {
	Collection string          `json:"collection"`
	SourceID   string          `json:"sourceId"`
	Kind       string          `json:"kind"`
	ID         string          `json:"id"`
	VehicleID  string          `json:"vehicleId,omitempty"`
	Raw        json.RawMessage `json:"raw"`
	Data       json.RawMessage `json:"data"`
}

type ImportBatch struct {
	Source   string          `json:"source"`
	Settings json.RawMessage `json:"settings"`
	Items    []ImportItem    `json:"items"`
}

type ImportReport struct {
	Created      int    `json:"created"`
	Updated      int    `json:"updated"`
	Skipped      int    `json:"skipped"`
	Conflicts    int    `json:"conflicts"`
	Retained     int    `json:"retained"`
	PreviewToken string `json:"previewToken"`
	Applied      bool   `json:"applied"`
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func ImportID(source, collection, id string) string {
	b, _ := json.Marshal([]string{source, collection, id})
	return "ll_" + digest(b)
}

func canonical(data json.RawMessage) (json.RawMessage, error) {
	if err := jsonutil.Validate(data); err != nil {
		return nil, err
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("expected one JSON document")
	}
	return json.Marshal(v)
}

// Import performs preview and application under the same database transaction.
// A missing target is a conflict, so rerunning cannot resurrect a user's deletion.
func (s *Store) Import(ctx context.Context, batch ImportBatch, applyToken string) (report ImportReport, err error) {
	ctx, done := operation(ctx, "db.migration.reconcile")
	defer func() { done(err) }()
	if batch.Source == "" || len(batch.Source) > 100 || len(batch.Items) == 0 || len(batch.Items) > 100000 {
		return report, errors.New("invalid migration batch")
	}
	if len(batch.Settings) == 0 {
		batch.Settings = json.RawMessage(`{}`)
	}
	if len(batch.Settings) > 4096 {
		return report, errors.New("migration settings exceed allowed length")
	}
	batch.Settings, err = canonical(batch.Settings)
	if err != nil || len(batch.Settings) == 0 || batch.Settings[0] != '{' {
		return report, errors.New("migration settings must be a JSON object")
	}
	sort.Slice(batch.Items, func(i, j int) bool {
		if batch.Items[i].Kind == "vehicle" && batch.Items[j].Kind != "vehicle" {
			return true
		}
		if batch.Items[j].Kind == "vehicle" && batch.Items[i].Kind != "vehicle" {
			return false
		}
		return batch.Items[i].ID < batch.Items[j].ID
	})
	seen := map[string]bool{}
	for i := range batch.Items {
		v := &batch.Items[i]
		if v.ID != ImportID(batch.Source, v.Collection, v.SourceID) || seen[v.ID] || v.SourceID == "" {
			return report, errors.New("invalid migration identity")
		}
		seen[v.ID] = true
		if v.Raw, err = canonical(v.Raw); err != nil {
			return report, errors.New("invalid source document")
		}
		if v.Data, err = canonical(v.Data); err != nil {
			return report, errors.New("invalid migration document")
		}
		switch v.Kind {
		case "vehicle":
			var target Vehicle
			if json.Unmarshal(v.Data, &target) != nil || target.ID != v.ID || target.Validate() != nil {
				return report, errors.New("invalid migration vehicle")
			}
		case "record":
			var target Record
			if json.Unmarshal(v.Data, &target) != nil || target.ID != v.ID || target.VehicleID != v.VehicleID || target.Validate() != nil {
				return report, errors.New("invalid migration record")
			}
		case "reminder":
			var target Reminder
			if json.Unmarshal(v.Data, &target) != nil || target.ID != v.ID || target.VehicleID != v.VehicleID || target.Validate() != nil {
				return report, errors.New("invalid migration reminder")
			}
		case "archive":
		default:
			return report, errors.New("invalid migration target kind")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	var existingSettings []byte
	err = tx.QueryRowContext(ctx, "SELECT settings FROM import_settings WHERE source=?", batch.Source).Scan(&existingSettings)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return report, err
	}
	if err == nil && !bytes.Equal(existingSettings, batch.Settings) {
		return report, ErrImportSettingsChanged
	}
	input, _ := json.Marshal(batch)
	h := sha256.New()
	h.Write(input)
	type mutation struct {
		item                   ImportItem
		sourceHash, targetHash string
		create                 bool
	}
	var changes []mutation
	for _, v := range batch.Items {
		sourceDocument, _ := json.Marshal([]json.RawMessage{v.Raw, v.Data})
		sourceHash := digest(sourceDocument)
		targetHash := digest(v.Data)
		var oldSource, oldTarget, oldID, oldKind string
		e := tx.QueryRowContext(ctx, "SELECT source_hash,target_hash,target_id,target_kind FROM import_sources WHERE source=? AND collection=? AND source_id=?", batch.Source, v.Collection, v.SourceID).Scan(&oldSource, &oldTarget, &oldID, &oldKind)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return report, e
		}
		isNew := errors.Is(e, sql.ErrNoRows)
		var current []byte
		if v.Kind == "vehicle" {
			e = tx.QueryRowContext(ctx, "SELECT data FROM vehicles WHERE id=?", v.ID).Scan(&current)
		} else if v.Kind != "archive" {
			e = tx.QueryRowContext(ctx, "SELECT data FROM entries WHERE id=?", v.ID).Scan(&current)
		} else {
			current = v.Data
			e = nil
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return report, e
		}
		var currentHash string
		if e == nil {
			c, ce := canonical(current)
			if ce != nil {
				return report, ce
			}
			currentHash = digest(c)
		}
		state, _ := json.Marshal([]string{v.ID, oldSource, oldTarget, currentHash})
		h.Write(state)
		if isNew {
			if v.Kind != "archive" && e == nil {
				report.Conflicts++
				continue
			}
			report.Created++
			changes = append(changes, mutation{v, sourceHash, targetHash, true})
			continue
		}
		if oldID != v.ID || oldKind != v.Kind || errors.Is(e, sql.ErrNoRows) {
			report.Conflicts++
			continue
		}
		if sourceHash == oldSource {
			report.Skipped++
			continue
		}
		if v.Kind != "archive" && currentHash != oldTarget {
			report.Conflicts++
			continue
		}
		report.Updated++
		changes = append(changes, mutation{v, sourceHash, targetHash, false})
	}
	rows, err := tx.QueryContext(ctx, "SELECT target_id FROM import_sources WHERE source=? ORDER BY target_id", batch.Source)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return report, err
		}
		if !seen[id] {
			report.Retained++
			h.Write([]byte(id))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	report.PreviewToken = hex.EncodeToString(h.Sum(nil))
	if applyToken == "" {
		return report, nil
	}
	if report.Conflicts > 0 || applyToken != report.PreviewToken {
		return report, ErrImportConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO import_settings(source,settings) VALUES(?,?) ON CONFLICT(source) DO NOTHING", batch.Source, string(batch.Settings)); err != nil {
		return report, err
	}
	for _, change := range changes {
		v := change.item
		if v.Kind == "vehicle" {
			_, err = tx.ExecContext(ctx, "INSERT INTO vehicles(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", v.ID, string(v.Data))
		} else if v.Kind != "archive" {
			_, err = tx.ExecContext(ctx, "INSERT INTO entries(id,vehicle_id,kind,data) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,vehicle_id=excluded.vehicle_id", v.ID, v.VehicleID, v.Kind, string(v.Data))
		}
		if err != nil {
			return report, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO import_sources(source,collection,source_id,target_id,target_kind,source_hash,target_hash,raw) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source,collection,source_id) DO UPDATE SET source_hash=excluded.source_hash,target_hash=excluded.target_hash,raw=excluded.raw`, batch.Source, v.Collection, v.SourceID, v.ID, v.Kind, change.sourceHash, change.targetHash, string(v.Raw))
		if err != nil {
			return report, err
		}
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	report.Applied = true
	return report, nil
}
