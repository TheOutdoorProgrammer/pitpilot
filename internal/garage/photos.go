package garage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image/jpeg"
)

const MaxVehiclePhotoBytes = 2 << 20

var ErrInvalidPhoto = errors.New("photo must be a JPEG up to 2 MiB and 1600 pixels per side")

// VehiclePhoto stays in the same database as its owner so a consistent backup
// cannot restore a vehicle pointing at a missing filesystem object.
type VehiclePhoto struct {
	VehicleID   string `json:"vehicleId"`
	Revision    string `json:"revision"`
	ContentType string `json:"contentType"`
	Data        []byte `json:"data"`
}

func initializeVehiclePhotos(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS vehicle_photos(vehicle_id TEXT PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE, revision TEXT NOT NULL, data BLOB NOT NULL)`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 6 {
		if _, err = tx.Exec("PRAGMA user_version=6"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func normalizeVehiclePhoto(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxVehiclePhotoBytes {
		return nil, ErrInvalidPhoto
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1600 || config.Height > 1600 {
		return nil, ErrInvalidPhoto
	}
	im, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidPhoto
	}
	// Decode and re-encode even native uploads: EXIF location and arbitrary
	// trailing payloads must not survive a different client's upload.
	var out bytes.Buffer
	if err = jpeg.Encode(&out, im, &jpeg.Options{Quality: 85}); err != nil || out.Len() > MaxVehiclePhotoBytes {
		return nil, ErrInvalidPhoto
	}
	return out.Bytes(), nil
}

func (s *Store) PutVehiclePhoto(ctx context.Context, id string, data []byte) (v Vehicle, err error) {
	ctx, done := operation(ctx, "db.vehicle.photo.put")
	defer func() { done(err) }()
	data, err = normalizeVehiclePhoto(data)
	if err != nil {
		return v, err
	}
	hash := sha256.Sum256(data)
	revision := hex.EncodeToString(hash[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE vehicles SET data=json_set(data,'$.photoRevision',?) WHERE id=?`, revision, id)
	if err = affected(result, err); err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO vehicle_photos(vehicle_id,revision,data) VALUES(?,?,?) ON CONFLICT(vehicle_id) DO UPDATE SET revision=excluded.revision,data=excluded.data`, id, revision, data); err != nil {
		return v, err
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	return s.Vehicle(ctx, id)
}

func (s *Store) VehiclePhoto(ctx context.Context, id string) (out VehiclePhoto, err error) {
	ctx, done := operation(ctx, "db.vehicle.photo.get")
	defer func() { done(err) }()
	out.VehicleID, out.ContentType = id, "image/jpeg"
	err = s.db.QueryRowContext(ctx, `SELECT revision,data FROM vehicle_photos WHERE vehicle_id=?`, id).Scan(&out.Revision, &out.Data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteVehiclePhoto(ctx context.Context, id string) (v Vehicle, err error) {
	ctx, done := operation(ctx, "db.vehicle.photo.delete")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE vehicles SET data=json_remove(data,'$.photoRevision') WHERE id=?`, id)
	if err = affected(result, err); err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM vehicle_photos WHERE vehicle_id=?`, id); err != nil {
		return v, err
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	return s.Vehicle(ctx, id)
}

func exportVehiclePhotos(ctx context.Context, tx *sql.Tx) ([]VehiclePhoto, error) {
	rows, err := tx.QueryContext(ctx, `SELECT vehicle_id,revision,data FROM vehicle_photos ORDER BY vehicle_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VehiclePhoto{}
	for rows.Next() {
		p := VehiclePhoto{ContentType: "image/jpeg"}
		if err = rows.Scan(&p.VehicleID, &p.Revision, &p.Data); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
