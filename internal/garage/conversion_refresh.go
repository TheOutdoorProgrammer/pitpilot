package garage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Versions retain the source and exact normalized rows replaced by reconciliation.
// A monotonic version also preserves a source that later reverts to older content.
type ConvertedSignalNoteVersion struct {
	Version           int                 `json:"version"`
	ConversionVersion int                 `json:"conversionVersion"`
	SourceHash        string              `json:"sourceHash"`
	SourceRaw         json.RawMessage     `json:"sourceRaw"`
	Archive           ConvertedSignalNote `json:"archive"`
	StoredBatch       SignalBatch         `json:"storedBatch"`
	StoredBatchHash   string              `json:"storedBatchHash"`
}

func refreshConvertedSummary(ctx context.Context, tx *sql.Tx, source string, item ImportItem, sourceHash, targetHash string, replacement SignalBatch) (state []byte, err error) {
	if err = requireLegacySignalsAllowed(ctx, tx, item.VehicleID); err != nil {
		return nil, err
	}
	// A failed candidate must not affect later candidates in the same preview.
	if _, err = tx.ExecContext(ctx, "SAVEPOINT converted_refresh"); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO converted_refresh")
			err = errors.Join(err, rollbackErr)
		}
		_, releaseErr := tx.ExecContext(ctx, "RELEASE converted_refresh")
		err = errors.Join(err, releaseErr)
	}()
	var raw []byte
	var originalHash, batchHash string
	if err = tx.QueryRowContext(ctx, "SELECT original_hash,batch_hash,data FROM converted_signal_notes WHERE note_id=?", item.ID).Scan(&originalHash, &batchHash, &raw); err != nil {
		return nil, err
	}
	var archive ConvertedSignalNote
	if json.Unmarshal(raw, &archive) != nil {
		return nil, ErrSignalConversionConflict
	}
	oldOriginalHash, e := SignalNoteHash(archive.Original)
	oldBatchJSON, marshalErr := json.Marshal(archive.Batch)
	identity := sha256.Sum256([]byte(item.ID))
	batchID := fmt.Sprintf("summary-%x", identity[:16])
	if e != nil || marshalErr != nil || oldOriginalHash != originalHash || originalHash != targetHash || digest(oldBatchJSON) != batchHash || archive.NoteID != item.ID || archive.VehicleID != item.VehicleID || item.Kind != "record" || item.Collection != "notes" || archive.Batch.Source != "lubelogger" || replacement.Source != "lubelogger" || archive.Batch.BatchID != batchID || replacement.BatchID != batchID || replacement.Validate() != nil {
		return nil, ErrSignalConversionConflict
	}
	old, err := normalizeSignalBatch(archive.Batch)
	if err != nil {
		return nil, err
	}
	version := ConvertedSignalNoteVersion{ConversionVersion: 1, SourceHash: sourceHash, Archive: archive, StoredBatch: old}
	var sourceRaw []byte
	if err = tx.QueryRowContext(ctx, "SELECT raw FROM import_sources WHERE source=? AND collection=? AND source_id=?", source, item.Collection, item.SourceID).Scan(&sourceRaw); err != nil {
		return nil, err
	}
	version.SourceRaw = sourceRaw
	oldProjection, e := canonical(archive.Original)
	if e != nil {
		return nil, e
	}
	oldSource, e := canonical(version.SourceRaw)
	if e != nil {
		return nil, e
	}
	oldDocument, _ := json.Marshal([]json.RawMessage{oldSource, oldProjection})
	if digest(oldDocument) != sourceHash {
		return nil, ErrSignalConversionConflict
	}
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM converted_signal_note_versions WHERE note_id=?", item.ID).Scan(&version.Version); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT hash FROM signal_batches WHERE vehicle_id=? AND source=? AND batch_id=?", item.VehicleID, old.Source, old.BatchID).Scan(&version.StoredBatchHash); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSignalConversionConflict
	} else if err != nil {
		return nil, err
	}
	normalized, _ := json.Marshal(old)
	if version.StoredBatchHash != digest(normalized) {
		return nil, ErrSignalConversionConflict
	}
	// Only archived rows with this note's deterministic namespace are owned here.
	for _, observation := range old.Observations {
		expected, _ := json.Marshal(observation)
		if err = deleteConvertedRow(ctx, tx, "signals", item.VehicleID, old.Source, batchID, observation.Key, expected); err != nil {
			return nil, err
		}
	}
	for _, signalContext := range old.Contexts {
		expected, _ := json.Marshal(signalContext)
		if err = deleteConvertedRow(ctx, tx, "signal_contexts", item.VehicleID, old.Source, batchID, signalContext.Key, expected); err != nil {
			return nil, err
		}
	}
	// Refuse even identical replacement collisions: their ownership is not ours.
	for _, observation := range replacement.Observations {
		if err = requireUnusedConvertedKey(ctx, tx, "signals", item.VehicleID, replacement.Source, batchID, observation.Key); err != nil {
			return nil, err
		}
	}
	for _, signalContext := range replacement.Contexts {
		if err = requireUnusedConvertedKey(ctx, tx, "signal_contexts", item.VehicleID, replacement.Source, batchID, signalContext.Key); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM signal_batches WHERE vehicle_id=? AND source=? AND batch_id=?", item.VehicleID, old.Source, batchID); err != nil {
		return nil, err
	}
	if _, err = ingestSignalsTx(ctx, tx, item.VehicleID, replacement); err != nil {
		return nil, err
	}
	state, err = json.Marshal(version)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO converted_signal_note_versions(note_id,version,data) VALUES(?,?,?)", item.ID, version.Version, string(state)); err != nil {
		return nil, err
	}
	updated := ConvertedSignalNote{NoteID: item.ID, VehicleID: item.VehicleID, Original: item.Data, Batch: replacement, ConvertedAt: time.Now().UTC()}
	updatedJSON, err := json.Marshal(updated)
	if err != nil {
		return nil, err
	}
	replacementJSON, err := json.Marshal(replacement)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE converted_signal_notes SET original_hash=?,batch_hash=?,data=? WHERE note_id=?", digest(item.Data), digest(replacementJSON), string(updatedJSON), item.ID)
	return state, err
}

func deleteConvertedRow(ctx context.Context, tx *sql.Tx, table, vehicleID, source, batchID, key string, expected []byte) error {
	if !strings.HasPrefix(key, batchID+"-") {
		return ErrSignalConversionConflict
	}
	var current []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM "+table+" WHERE vehicle_id=? AND source=? AND key=?", vehicleID, source, key).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSignalConversionConflict
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, current) {
		return ErrSignalConversionConflict
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE vehicle_id=? AND source=? AND key=?", vehicleID, source, key)
	return err
}

func requireUnusedConvertedKey(ctx context.Context, tx *sql.Tx, table, vehicleID, source, batchID, key string) error {
	if !strings.HasPrefix(key, batchID+"-") {
		return ErrSignalConversionConflict
	}
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE vehicle_id=? AND source=? AND key=?", vehicleID, source, key).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrSignalConversionConflict
}
