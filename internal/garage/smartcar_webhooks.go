package garage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

func initializeSmartcarWebhooks(db *sql.DB) error {
	_, err := db.Exec(`BEGIN;
	CREATE TABLE IF NOT EXISTS smartcar_webhook_receipts(connection_id TEXT NOT NULL REFERENCES smartcar_connections(id) ON DELETE CASCADE, event_key TEXT NOT NULL, content_hash TEXT NOT NULL, received_at INTEGER NOT NULL, PRIMARY KEY(connection_id,event_key));
	PRAGMA user_version=9;
	COMMIT;`)
	return err
}

type SmartcarWebhookUpdate struct {
	EventKey, ContentHash, EventType string
	ReceivedAt                       time.Time
	Errors                           int
	Batch                            SignalBatch
	Latest                           *time.Time
	Metrics                          []string
}

func mergeSmartcarMetrics(a, b []string) []string {
	out := append(slices.Clone(a), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

func (s *Store) SmartcarConnectionByRemoteKey(ctx context.Context, key string) (v SmartcarConnection, err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.connection.lookup")
	defer func() { done(err) }()
	return scanSmartcar(s.db.QueryRowContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections WHERE remote_key=?`, key))
}

// Receipt, measurements and status commit together; retries must never acknowledge lost data.
func (s *Store) ApplySmartcarWebhook(ctx context.Context, expected SmartcarConnection, update SmartcarWebhookUpdate) (duplicate bool, err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.webhook.apply")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	v, err := scanSmartcar(tx.QueryRowContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections WHERE id=? AND remote_key=?`, expected.ID, expected.RemoteKey))
	if err != nil {
		return false, err
	}
	var hash string
	err = tx.QueryRowContext(ctx, `SELECT content_hash FROM smartcar_webhook_receipts WHERE connection_id=? AND event_key=?`, v.ID, update.EventKey).Scan(&hash)
	if err == nil {
		if hash != update.ContentHash {
			return false, ErrSignalConflict
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if len(update.Batch.Observations) > 0 || len(update.Batch.Contexts) > 0 {
		if _, err = ingestSignalsTx(ctx, tx, v.VehicleID, update.Batch); err != nil {
			return false, err
		}
	}
	v.Status.LastWebhookAt = &update.ReceivedAt
	v.Status.LastWebhookEventType = update.EventType
	v.Status.WebhookErrors = update.Errors
	if update.Latest != nil {
		v.Status.LastSuccessAt = &update.ReceivedAt
		v.Status.SupportedMetrics = mergeSmartcarMetrics(v.Status.SupportedMetrics, update.Metrics)
		if v.Status.LatestObservedAt == nil || update.Latest.After(*v.Status.LatestObservedAt) {
			v.Status.LatestObservedAt = update.Latest
			v.Status.State, v.Status.ErrorCode = "connected", ""
		}
	}
	raw, err := json.Marshal(v.Status)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE smartcar_connections SET status=? WHERE id=?`, string(raw), v.ID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO smartcar_webhook_receipts(connection_id,event_key,content_hash,received_at) VALUES(?,?,?,?)`, v.ID, update.EventKey, update.ContentHash, update.ReceivedAt.Unix()); err != nil {
		return false, err
	}
	return false, tx.Commit()
}
