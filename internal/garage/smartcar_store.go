package garage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrSmartcarConflict = errors.New("smartcar integration state changed")

const MinimumSmartcarPollInterval = time.Hour

func smartcarOperation(ctx context.Context, name string) (context.Context, func(error)) {
	ctx, done := operation(ctx, "db."+name)
	return ctx, func(err error) {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrSmartcarConflict) {
			err = nil
		}
		done(err)
	}
}

// Private integration state belongs in encrypted database backups, not garage JSON exports.
type SmartcarSession struct {
	ID, VehicleID string
	ExpiresAt     time.Time
	Encrypted     []byte
	Version       int
}

type SmartcarStatus struct {
	State                  string     `json:"state"`
	AuthorizationStartedAt *time.Time `json:"authorizationStartedAt,omitempty"`
	ConnectionID           string     `json:"connectionId,omitempty"`
	LastAttemptAt          *time.Time `json:"lastAttemptAt,omitempty"`
	LastSuccessAt          *time.Time `json:"lastSuccessAt,omitempty"`
	LatestObservedAt       *time.Time `json:"latestObservedAt,omitempty"`
	NextAttemptAt          *time.Time `json:"nextAttemptAt,omitempty"`
	ErrorCode              string     `json:"errorCode,omitempty"`
	SupportedMetrics       []string   `json:"supportedMetrics"`
	UnavailableSignals     int        `json:"unavailableSignals"`
	UnsupportedSignals     int        `json:"unsupportedSignals"`
	LastWebhookAt          *time.Time `json:"lastWebhookAt,omitempty"`
	LastWebhookEventType   string     `json:"lastWebhookEventType,omitempty"`
	WebhookErrors          int        `json:"webhookErrors"`
}

type SmartcarConnection struct {
	ID, VehicleID, RemoteKey string
	Encrypted                []byte
	Status                   SmartcarStatus
	NextAttemptAt, RetryAt   time.Time
	LeaseToken               string
	Failures                 int
}

func initializeSmartcar(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS smartcar_sessions(id TEXT PRIMARY KEY, vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, expires_at INTEGER NOT NULL, encrypted BLOB NOT NULL, version INTEGER NOT NULL DEFAULT 0);
	CREATE INDEX IF NOT EXISTS smartcar_sessions_expiry ON smartcar_sessions(expires_at);
	CREATE TABLE IF NOT EXISTS smartcar_connections(id TEXT NOT NULL UNIQUE, vehicle_id TEXT PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE, remote_key TEXT NOT NULL UNIQUE, encrypted BLOB NOT NULL, status TEXT NOT NULL CHECK(json_valid(status)), next_attempt INTEGER NOT NULL, retry_at INTEGER NOT NULL DEFAULT 0, lease_until INTEGER NOT NULL DEFAULT 0, lease_token TEXT NOT NULL DEFAULT '', failures INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE IF NOT EXISTS smartcar_control(name TEXT PRIMARY KEY, until_time INTEGER NOT NULL);`)
	return err
}

func (s *Store) CreateSmartcarSession(ctx context.Context, v SmartcarSession) (err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.session.create")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM smartcar_sessions WHERE expires_at<=?`, time.Now().Unix()); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM smartcar_sessions WHERE vehicle_id=?`, v.VehicleID).Scan(&count); err != nil {
		return err
	}
	if count >= 10 {
		return ErrSmartcarConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO smartcar_sessions(id,vehicle_id,expires_at,encrypted) VALUES(?,?,?,?)`, v.ID, v.VehicleID, v.ExpiresAt.Unix(), v.Encrypted); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SmartcarSession(ctx context.Context, id, vehicleID string) (v SmartcarSession, err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.session.read")
	defer func() { done(err) }()
	v.ID = id
	v.VehicleID = vehicleID
	var expiry int64
	err = s.db.QueryRowContext(ctx, `SELECT expires_at,encrypted,version FROM smartcar_sessions WHERE id=? AND vehicle_id=?`, id, vehicleID).Scan(&expiry, &v.Encrypted, &v.Version)
	v.ExpiresAt = time.Unix(expiry, 0).UTC()
	return
}

func (s *Store) UpdateSmartcarSession(ctx context.Context, v SmartcarSession) (err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.session.update")
	defer func() { done(err) }()
	r, err := s.db.ExecContext(ctx, `UPDATE smartcar_sessions SET encrypted=?,version=version+1 WHERE id=? AND vehicle_id=? AND version=? AND expires_at>?`, v.Encrypted, v.ID, v.VehicleID, v.Version, time.Now().Unix())
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrSmartcarConflict
	}
	return nil
}

const smartcarColumns = `id,vehicle_id,remote_key,encrypted,status,next_attempt,retry_at,lease_token,failures`

func scanSmartcar(row interface{ Scan(...any) error }) (v SmartcarConnection, err error) {
	var raw string
	var next, retry int64
	err = row.Scan(&v.ID, &v.VehicleID, &v.RemoteKey, &v.Encrypted, &raw, &next, &retry, &v.LeaseToken, &v.Failures)
	if err != nil {
		return
	}
	err = json.Unmarshal([]byte(raw), &v.Status)
	v.NextAttemptAt = time.Unix(next, 0).UTC()
	v.RetryAt = time.Unix(retry, 0).UTC()
	return
}
func (s *Store) SmartcarConnection(ctx context.Context, vehicleID string) (v SmartcarConnection, err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.connection.read")
	defer func() { done(err) }()
	return scanSmartcar(s.db.QueryRowContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections WHERE vehicle_id=?`, vehicleID))
}
func (s *Store) SmartcarConnections(ctx context.Context) (out []SmartcarConnection, err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.connection.list")
	defer func() { done(err) }()
	rows, err := s.db.QueryContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections ORDER BY vehicle_id`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var v SmartcarConnection
		v, err = scanSmartcar(rows)
		if err != nil {
			return
		}
		out = append(out, v)
	}
	err = rows.Err()
	return
}

func (s *Store) BindSmartcar(ctx context.Context, session SmartcarSession, expectedID string, v SmartcarConnection) (err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.connection.bind")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var version int
	var expiry int64
	if err = tx.QueryRowContext(ctx, `SELECT version,expires_at FROM smartcar_sessions WHERE id=? AND vehicle_id=?`, session.ID, v.VehicleID).Scan(&version, &expiry); err != nil {
		return
	}
	if version != session.Version || expiry <= time.Now().Unix() {
		return ErrSmartcarConflict
	}
	previous, readErr := scanSmartcar(tx.QueryRowContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections WHERE vehicle_id=?`, v.VehicleID))
	err = readErr
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if previous.ID != expectedID {
		return ErrSmartcarConflict
	}
	if previous.ID != "" {
		// Reauthorization replaces the connection generation; old receipts must
		// not prevent its ID changing or authenticate deliveries for the new binding.
		if _, err = tx.ExecContext(ctx, `DELETE FROM smartcar_webhook_receipts WHERE connection_id=?`, previous.ID); err != nil {
			return err
		}
		v.Status.LastAttemptAt = previous.Status.LastAttemptAt
		v.RetryAt = previous.RetryAt
		if previous.NextAttemptAt.After(v.NextAttemptAt) {
			v.NextAttemptAt = previous.NextAttemptAt
		}
		if v.RetryAt.After(v.NextAttemptAt) {
			v.NextAttemptAt = v.RetryAt
		}
		v.Status.NextAttemptAt = &v.NextAttemptAt
	}
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT vehicle_id FROM smartcar_connections WHERE remote_key=?`, v.RemoteKey).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if owner != "" && owner != v.VehicleID {
		return ErrSmartcarConflict
	}
	raw, err := json.Marshal(v.Status)
	if err != nil {
		return
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO smartcar_connections(id,vehicle_id,remote_key,encrypted,status,next_attempt,retry_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(vehicle_id) DO UPDATE SET id=excluded.id,remote_key=excluded.remote_key,encrypted=excluded.encrypted,status=excluded.status,next_attempt=excluded.next_attempt,retry_at=excluded.retry_at,lease_until=0,lease_token='',failures=0`, v.ID, v.VehicleID, v.RemoteKey, v.Encrypted, string(raw), v.NextAttemptAt.Unix(), max(int64(0), v.RetryAt.Unix()))
	if err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM smartcar_sessions WHERE vehicle_id=?`, v.VehicleID); err != nil {
		return
	}
	return tx.Commit()
}

func (s *Store) DetachSmartcar(ctx context.Context, vehicleID string) (err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.connection.detach")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM smartcar_sessions WHERE vehicle_id=?`, vehicleID); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM smartcar_connections WHERE vehicle_id=?`, vehicleID); err != nil {
		return
	}
	return tx.Commit()
}

func (s *Store) ClaimSmartcar(ctx context.Context, now time.Time, interval time.Duration) (v SmartcarConnection, err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.sync.claim")
	defer func() { done(err) }()
	interval = max(interval, MinimumSmartcarPollInterval)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var backoff int64
	if e := tx.QueryRowContext(ctx, `SELECT until_time FROM smartcar_control WHERE name='app_backoff'`).Scan(&backoff); e != nil && !errors.Is(e, sql.ErrNoRows) {
		err = e
		return
	}
	if backoff > now.Unix() {
		err = sql.ErrNoRows
		return
	}
	v, err = scanSmartcar(tx.QueryRowContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections WHERE next_attempt<=? AND retry_at<=? AND lease_until<=? AND coalesce(unixepoch(json_extract(status,'$.lastAttemptAt')),0)<=? ORDER BY next_attempt LIMIT 1`, now.Unix(), now.Unix(), now.Unix(), now.Add(-interval).Unix()))
	if err != nil {
		return
	}
	v.LeaseToken = NewID()
	// Persist the configured cooldown before contacting Smartcar so crashes cannot bypass it.
	now = now.UTC()
	v.Status.LastAttemptAt = &now
	v.NextAttemptAt = now.Add(interval)
	v.Status.NextAttemptAt = &v.NextAttemptAt
	raw, err := json.Marshal(v.Status)
	if err != nil {
		return v, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE smartcar_connections SET lease_until=?,lease_token=?,status=?,next_attempt=? WHERE id=?`, now.Add(2*time.Minute).Unix(), v.LeaseToken, string(raw), v.NextAttemptAt.Unix(), v.ID)
	if err != nil {
		return
	}
	err = tx.Commit()
	return
}

// The lease check and signal writes share a transaction so a detached in-flight job cannot publish data.
func (s *Store) FinishSmartcar(ctx context.Context, v SmartcarConnection, batch *SignalBatch, appBackoff time.Time) (err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.sync.finish")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	current, err := scanSmartcar(tx.QueryRowContext(ctx, `SELECT `+smartcarColumns+` FROM smartcar_connections WHERE id=? AND vehicle_id=?`, v.ID, v.VehicleID))
	if err != nil {
		return
	}
	if current.LeaseToken == "" || current.LeaseToken != v.LeaseToken {
		return ErrSmartcarConflict
	}
	if batch != nil && (len(batch.Observations) > 0 || len(batch.Contexts) > 0) {
		if _, err = ingestSignalsTx(ctx, tx, v.VehicleID, *batch); err != nil {
			return
		}
	}
	// A webhook can commit while this worker is waiting for the provider.
	v.Status.LastWebhookAt = current.Status.LastWebhookAt
	v.Status.LastWebhookEventType = current.Status.LastWebhookEventType
	v.Status.WebhookErrors = current.Status.WebhookErrors
	v.Status.SupportedMetrics = mergeSmartcarMetrics(v.Status.SupportedMetrics, current.Status.SupportedMetrics)
	if current.Status.LastSuccessAt != nil && (v.Status.LastSuccessAt == nil || current.Status.LastSuccessAt.After(*v.Status.LastSuccessAt)) {
		v.Status.LastSuccessAt = current.Status.LastSuccessAt
	}
	if current.Status.LatestObservedAt != nil && (v.Status.LatestObservedAt == nil || current.Status.LatestObservedAt.After(*v.Status.LatestObservedAt)) {
		v.Status.LatestObservedAt = current.Status.LatestObservedAt
		if v.Status.ErrorCode == "no_timestamped_signals" {
			v.Status.State, v.Status.ErrorCode = current.Status.State, current.Status.ErrorCode
		}
	}
	raw, err := json.Marshal(v.Status)
	if err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE smartcar_connections SET status=?,next_attempt=?,retry_at=?,lease_until=0,lease_token='',failures=? WHERE id=?`, string(raw), v.NextAttemptAt.Unix(), v.RetryAt.Unix(), v.Failures, v.ID); err != nil {
		return
	}
	if !appBackoff.IsZero() {
		if _, err = tx.ExecContext(ctx, `INSERT INTO smartcar_control(name,until_time) VALUES('app_backoff',?) ON CONFLICT(name) DO UPDATE SET until_time=max(until_time,excluded.until_time)`, appBackoff.Unix()); err != nil {
			return
		}
	}
	return tx.Commit()
}

func (s *Store) RequestSmartcarSync(ctx context.Context, vehicleID string, now time.Time) (err error) {
	ctx, done := smartcarOperation(ctx, "smartcar.sync.request")
	defer func() { done(err) }()
	// Human refresh cannot bypass OEM backoff or hammer a vehicle repeatedly.
	r, err := s.db.ExecContext(ctx, `UPDATE smartcar_connections SET next_attempt=max(next_attempt,?,retry_at,coalesce(unixepoch(json_extract(status,'$.lastAttemptAt')),0)+?) WHERE vehicle_id=?`, now.Unix(), int64(MinimumSmartcarPollInterval.Seconds()), vehicleID)
	if err != nil {
		return
	}
	return affected(r, nil)
}
