package garage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrLegacySignalsDiscarded = errors.New("legacy signal history was discarded for this vehicle")

type LegacySignalPolicy struct {
	VehicleID   string    `json:"vehicleId"`
	DiscardedAt time.Time `json:"discardedAt"`
}

func initializeLegacySignalPolicies(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS legacy_signal_policies(vehicle_id TEXT PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE,discarded_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 8 {
		if _, err = tx.Exec("PRAGMA user_version=8"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func requireLegacySignalsAllowed(ctx context.Context, tx *sql.Tx, vehicleID string) error {
	var discarded int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM legacy_signal_policies WHERE vehicle_id=?", vehicleID).Scan(&discarded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrLegacySignalsDiscarded
}

func exportLegacySignalPolicies(ctx context.Context, tx *sql.Tx) ([]LegacySignalPolicy, error) {
	result := []LegacySignalPolicy{}
	rows, err := tx.QueryContext(ctx, "SELECT vehicle_id,discarded_at FROM legacy_signal_policies ORDER BY vehicle_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p LegacySignalPolicy
		var at string
		if err = rows.Scan(&p.VehicleID, &at); err != nil {
			return nil, err
		}
		if p.DiscardedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
