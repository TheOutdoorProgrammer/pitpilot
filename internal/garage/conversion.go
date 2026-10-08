package garage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

var ErrSignalConversionConflict = errors.New("summary conversion preview is stale or conflicts with existing data")

type SignalNoteConversion struct {
	NoteID       string      `json:"noteId"`
	ExpectedHash string      `json:"expectedHash"`
	Batch        SignalBatch `json:"batch"`
}

type SignalConversionReport struct {
	Converted    int    `json:"converted"`
	Skipped      int    `json:"skipped"`
	PreviewToken string `json:"previewToken"`
	Applied      bool   `json:"applied"`
}

type ConvertedSignalNote struct {
	NoteID      string          `json:"noteId"`
	VehicleID   string          `json:"vehicleId"`
	Original    json.RawMessage `json:"original"`
	Batch       SignalBatch     `json:"batch"`
	ConvertedAt time.Time       `json:"convertedAt"`
}

func initializeSignalConversions(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS converted_signal_notes(
		note_id TEXT PRIMARY KEY,
		vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
		original_hash TEXT NOT NULL,
		batch_hash TEXT NOT NULL,
		data TEXT NOT NULL CHECK(json_valid(data))
	);`)
	return err
}

func SignalNoteHash(raw json.RawMessage) (string, error) {
	data, err := canonical(raw)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

// Conversion keeps evidence and signals in the same commit as visible-note removal.
// Preview runs the writes inside a rolled-back transaction to validate every constraint.
func (s *Store) ConvertSignalNotes(ctx context.Context, conversions []SignalNoteConversion, token string) (report SignalConversionReport, err error) {
	ctx, done := operation(ctx, "db.signals.convert_notes")
	defer func() { done(err) }()
	if len(conversions) > 10000 {
		return report, errors.New("too many summary conversions")
	}
	conversions = append([]SignalNoteConversion(nil), conversions...)
	sort.Slice(conversions, func(i, j int) bool { return conversions[i].NoteID < conversions[j].NoteID })
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	h := sha256.New()
	input, err := json.Marshal(conversions)
	if err != nil {
		return report, err
	}
	h.Write(input)
	seen := make(map[string]bool, len(conversions))
	for _, conversion := range conversions {
		if conversion.NoteID == "" || len(conversion.ExpectedHash) != 64 || seen[conversion.NoteID] {
			return report, errors.New("invalid summary conversion identity")
		}
		seen[conversion.NoteID] = true
		batchData, e := json.Marshal(conversion.Batch)
		if e != nil {
			return report, e
		}
		batchHash := digest(batchData)
		var oldHash, oldBatch string
		e = tx.QueryRowContext(ctx, "SELECT original_hash,batch_hash FROM converted_signal_notes WHERE note_id=?", conversion.NoteID).Scan(&oldHash, &oldBatch)
		if e == nil {
			if oldHash != conversion.ExpectedHash || oldBatch != batchHash {
				return report, ErrSignalConversionConflict
			}
			h.Write([]byte("archived:" + oldHash + oldBatch))
			report.Skipped++
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return report, e
		}
		var raw []byte
		var vehicleID string
		e = tx.QueryRowContext(ctx, "SELECT vehicle_id,data FROM entries WHERE id=? AND kind='record'", conversion.NoteID).Scan(&vehicleID, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			return report, ErrSignalConversionConflict
		}
		if e != nil {
			return report, e
		}
		currentHash, e := SignalNoteHash(raw)
		if e != nil {
			return report, e
		}
		var note Record
		if json.Unmarshal(raw, &note) != nil || note.Kind != "note" || currentHash != conversion.ExpectedHash {
			return report, ErrSignalConversionConflict
		}
		ingested, e := ingestSignalsTx(ctx, tx, vehicleID, conversion.Batch)
		if e != nil {
			return report, e
		}
		state, e := json.Marshal(ingested)
		if e != nil {
			return report, e
		}
		h.Write([]byte(currentHash))
		h.Write(state)
		archived := ConvertedSignalNote{conversion.NoteID, vehicleID, raw, conversion.Batch, time.Now().UTC()}
		data, e := json.Marshal(archived)
		if e != nil {
			return report, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO converted_signal_notes(note_id,vehicle_id,original_hash,batch_hash,data) VALUES(?,?,?,?,?)", conversion.NoteID, vehicleID, currentHash, batchHash, string(data)); e != nil {
			return report, e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM entries WHERE id=? AND kind='record'", conversion.NoteID); e != nil {
			return report, e
		}
		report.Converted++
	}
	report.PreviewToken = hex.EncodeToString(h.Sum(nil))
	if token == "" {
		return report, nil
	}
	if token != report.PreviewToken {
		return report, ErrSignalConversionConflict
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	report.Applied = true
	return report, nil
}

func exportSignalConversions(ctx context.Context, tx *sql.Tx) ([]ConvertedSignalNote, error) {
	out := []ConvertedSignalNote{}
	rows, err := tx.QueryContext(ctx, "SELECT data FROM converted_signal_notes ORDER BY note_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item ConvertedSignalNote
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
