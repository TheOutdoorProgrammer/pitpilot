package garage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var ErrDeviceUnauthorized = errors.New("device authorization expired or revoked")
var ErrDeviceLimit = errors.New("vehicle has too many active devices")

type Device struct {
	ID           string     `json:"id"`
	VehicleID    string     `json:"vehicleId"`
	Name         string     `json:"name"`
	CreatedAt    time.Time  `json:"createdAt"`
	AutoUpdate   bool       `json:"autoUpdate"`
	GPSRecording bool       `json:"gpsRecording"`
	EnrolledAt   *time.Time `json:"enrolledAt,omitempty"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	LastSeenAt   *time.Time `json:"lastSeenAt,omitempty"`
	*DeviceHeartbeat
}

type DeviceHeartbeat struct {
	LastObservedAt  *time.Time `json:"lastObservedAt,omitempty"`
	LastUploadAt    *time.Time `json:"lastUploadAt,omitempty"`
	Version         string     `json:"version,omitempty"`
	QueuedBatches   int64      `json:"queuedBatches"`
	RejectedSamples uint64     `json:"rejectedSamples"`
	CollectionState string     `json:"collectionState,omitempty"`
	UpdateState     string     `json:"updateState,omitempty"`
	GPSState        string     `json:"gpsState,omitempty"`
}

func (h DeviceHeartbeat) Validate() error {
	for _, at := range []*time.Time{h.LastObservedAt, h.LastUploadAt} {
		if at != nil && (at.IsZero() || at.Year() < 2020 || at.After(time.Now().Add(time.Minute))) {
			return errors.New("invalid collector status timestamp")
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`).MatchString(h.Version) || h.QueuedBatches < 0 || h.QueuedBatches > 1e9 || h.RejectedSamples > 1e15 {
		return errors.New("invalid collector status")
	}
	switch h.CollectionState {
	case "starting", "waiting_clock", "collecting", "adapter_unavailable", "paused", "queue_full":
	default:
		return errors.New("invalid collection state")
	}
	switch h.UpdateState {
	case "idle", "checking", "downloading", "staged", "applying", "healthy", "rolled_back", "failed", "disabled":
	default:
		return errors.New("invalid update state")
	}
	switch h.GPSState {
	case "", "disabled", "disconnected", "waiting_clock", "waiting_fix", "fix", "queue_full", "paused":
	default:
		return errors.New("invalid GPS state")
	}
	return nil
}

type DeviceEnrollment struct {
	Device          Device    `json:"device"`
	EnrollmentToken string    `json:"enrollmentToken"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type DeviceConfiguration struct {
	DeviceID        string `json:"deviceId"`
	VehicleID       string `json:"vehicleId"`
	AutoUpdate      bool   `json:"autoUpdate"`
	GPSRecording    bool   `json:"-"`
	ProtocolVersion int    `json:"protocolVersion"`
}

type EnrolledDevice struct {
	DeviceConfiguration
	Token string `json:"token"`
}

func initializeDevices(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS devices (
	 id TEXT PRIMARY KEY, vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
	 token_hash TEXT UNIQUE, enrollment_hash TEXT UNIQUE, enrollment_expires INTEGER NOT NULL,
	 revoked INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL CHECK(json_valid(data)));
	 CREATE INDEX IF NOT EXISTS devices_vehicle ON devices(vehicle_id);`)
	return err
}

func randomDeviceToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("entropy unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func deviceTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func ValidDeviceName(name string) bool {
	return name == strings.TrimSpace(name) && name != "" && len(name) <= 80 && !strings.ContainsAny(name, "\r\n\x00")
}

func (s *Store) CreateDevice(ctx context.Context, vehicleID, name string) (out DeviceEnrollment, err error) {
	ctx, done := operation(ctx, "db.device.create")
	defer func() { done(err) }()
	if !ValidDeviceName(name) {
		return out, errors.New("device name must contain 1 to 80 characters")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM vehicles WHERE id=?", vehicleID).Scan(&count); err != nil {
		return out, err
	}
	if count == 0 {
		return out, ErrNotFound
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM devices WHERE vehicle_id=? AND revoked=0", vehicleID).Scan(&count); err != nil {
		return out, err
	}
	if count >= 16 {
		return out, ErrDeviceLimit
	}
	now := time.Now().UTC()
	out = DeviceEnrollment{Device: Device{ID: NewID(), VehicleID: vehicleID, Name: name, CreatedAt: now, AutoUpdate: true}, EnrollmentToken: randomDeviceToken(), ExpiresAt: now.Add(15 * time.Minute)}
	raw, err := json.Marshal(out.Device)
	if err != nil {
		return DeviceEnrollment{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO devices(id,vehicle_id,enrollment_hash,enrollment_expires,data) VALUES(?,?,?,?,?)", out.Device.ID, vehicleID, deviceTokenHash(out.EnrollmentToken), out.ExpiresAt.Unix(), string(raw))
	if err != nil {
		return DeviceEnrollment{}, err
	}
	err = tx.Commit()
	return
}

func (s *Store) Devices(ctx context.Context, vehicleID string) (out []Device, err error) {
	ctx, done := operation(ctx, "db.devices.list")
	defer func() { done(err) }()
	if _, err = s.Vehicle(ctx, vehicleID); err != nil {
		return nil, err
	}
	out = []Device{}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM devices WHERE vehicle_id=? ORDER BY json_extract(data,'$.createdAt'),id", vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Device
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) EnrollDevice(ctx context.Context, token string) (out EnrolledDevice, err error) {
	ctx, done := operation(ctx, "db.device.enroll")
	defer func() { done(err) }()
	if len(token) != 43 {
		return out, ErrDeviceUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var raw []byte
	err = tx.QueryRowContext(ctx, "SELECT data FROM devices WHERE enrollment_hash=? AND enrollment_expires>? AND revoked=0 AND token_hash IS NULL", deviceTokenHash(token), time.Now().Unix()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDeviceUnauthorized
	}
	if err != nil {
		return out, err
	}
	var d Device
	if err = json.Unmarshal(raw, &d); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	d.EnrolledAt = &now
	out = EnrolledDevice{DeviceConfiguration: deviceConfiguration(d), Token: randomDeviceToken()}
	raw, err = json.Marshal(d)
	if err != nil {
		return EnrolledDevice{}, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE devices SET token_hash=?,enrollment_hash=NULL,enrollment_expires=0,data=? WHERE id=?", deviceTokenHash(out.Token), string(raw), d.ID)
	if err != nil {
		return EnrolledDevice{}, err
	}
	err = tx.Commit()
	return
}

func deviceConfiguration(d Device) DeviceConfiguration {
	return DeviceConfiguration{DeviceID: d.ID, VehicleID: d.VehicleID, AutoUpdate: d.AutoUpdate, GPSRecording: d.GPSRecording, ProtocolVersion: 1}
}

func readDevice(ctx context.Context, tx *sql.Tx, predicate, key string) (Device, error) {
	var d Device
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM devices WHERE "+predicate, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrDeviceUnauthorized
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(raw, &d)
	return d, err
}

func (s *Store) DeviceConfiguration(ctx context.Context, token string) (out DeviceConfiguration, err error) {
	ctx, done := operation(ctx, "db.device.config")
	defer func() { done(err) }()
	if len(token) != 43 {
		return out, ErrDeviceUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	d, err := readDevice(ctx, tx, "token_hash=? AND revoked=0", deviceTokenHash(token))
	if err != nil {
		return out, err
	}
	return deviceConfiguration(d), nil
}

func saveDevice(ctx context.Context, tx *sql.Tx, d Device) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE devices SET data=? WHERE id=?", string(raw), d.ID)
	return err
}

func (s *Store) SetDeviceAutoUpdate(ctx context.Context, id string, enabled bool) (out Device, err error) {
	return s.SetDevicePolicy(ctx, id, &enabled, nil)
}

func (s *Store) SetDevicePolicy(ctx context.Context, id string, autoUpdate, gpsRecording *bool) (out Device, err error) {
	ctx, done := operation(ctx, "db.device.policy")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = readDevice(ctx, tx, "id=? AND revoked=0", id)
	if errors.Is(err, ErrDeviceUnauthorized) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if autoUpdate != nil {
		out.AutoUpdate = *autoUpdate
	}
	if gpsRecording != nil {
		out.GPSRecording = *gpsRecording
	}
	if err = saveDevice(ctx, tx, out); err != nil {
		return out, err
	}
	err = tx.Commit()
	return
}

func (s *Store) RevokeDevice(ctx context.Context, id string) (err error) {
	ctx, done := operation(ctx, "db.device.revoke")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := readDevice(ctx, tx, "id=?", id)
	if errors.Is(err, ErrDeviceUnauthorized) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if d.RevokedAt == nil {
		now := time.Now().UTC()
		d.RevokedAt = &now
	}
	if err = saveDevice(ctx, tx, d); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE devices SET revoked=1,token_hash=NULL,enrollment_hash=NULL,enrollment_expires=0 WHERE id=?", id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) HeartbeatDevice(ctx context.Context, token string, h DeviceHeartbeat) (err error) {
	ctx, done := operation(ctx, "db.device.heartbeat")
	defer func() { done(err) }()
	if len(token) != 43 {
		return ErrDeviceUnauthorized
	}
	if err = h.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := readDevice(ctx, tx, "token_hash=? AND revoked=0", deviceTokenHash(token))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	d.LastSeenAt = &now
	if previous := d.DeviceHeartbeat; previous != nil {
		if previous.LastObservedAt != nil && (h.LastObservedAt == nil || h.LastObservedAt.Before(*previous.LastObservedAt)) {
			h.LastObservedAt = previous.LastObservedAt
		}
		if previous.LastUploadAt != nil && (h.LastUploadAt == nil || h.LastUploadAt.Before(*previous.LastUploadAt)) {
			h.LastUploadAt = previous.LastUploadAt
		}
	}
	d.DeviceHeartbeat = &h
	if err = saveDevice(ctx, tx, d); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IngestDeviceSignals(ctx context.Context, token string, batch SignalBatch) (report SignalIngestReport, err error) {
	ctx, done := operation(ctx, "db.device.signals")
	defer func() { done(err) }()
	if len(token) != 43 {
		return report, ErrDeviceUnauthorized
	}
	batch.Source = "pi"
	if err = batch.Validate(); err != nil {
		return report, err
	}
	batch, err = normalizeSignalBatch(batch)
	if err != nil {
		return report, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	d, err := readDevice(ctx, tx, "token_hash=? AND revoked=0", deviceTokenHash(token))
	if err != nil {
		return report, err
	}
	// Hash the caller's key to preserve the canonical length limit while isolating devices.
	key := func(raw string) string { return d.ID + ":" + deviceTokenHash(raw) }
	batch.BatchID = key(batch.BatchID)
	for i := range batch.Observations {
		batch.Observations[i].Key = key(batch.Observations[i].Key)
	}
	for i := range batch.Contexts {
		batch.Contexts[i].Key = key(batch.Contexts[i].Key)
		if location := batch.Contexts[i].Location; location != nil && location.Type == "gps" && location.RecordingID != "" {
			location.RecordingID = key(location.RecordingID)
		}
	}
	report, err = ingestSignalsTx(ctx, tx, d.VehicleID, batch)
	if err != nil {
		return report, err
	}
	now := time.Now().UTC()
	d.LastSeenAt = &now
	if err = saveDevice(ctx, tx, d); err != nil {
		return report, err
	}
	err = tx.Commit()
	return
}
