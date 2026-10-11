package garage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
)

type PowerSafeZone struct {
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	RadiusMeters float64 `json:"radiusMeters"`
}

func (z *PowerSafeZone) UnmarshalJSON(raw []byte) error {
	var fields struct {
		Latitude     *float64 `json:"latitude"`
		Longitude    *float64 `json:"longitude"`
		RadiusMeters *float64 `json:"radiusMeters"`
	}
	if decodePowerPolicyJSON(raw, &fields) != nil || fields.Latitude == nil || fields.Longitude == nil || fields.RadiusMeters == nil {
		return errors.New("power-safe zone requires latitude, longitude and radiusMeters")
	}
	zone := PowerSafeZone{Latitude: *fields.Latitude, Longitude: *fields.Longitude, RadiusMeters: *fields.RadiusMeters}
	if err := zone.Validate(); err != nil {
		return err
	}
	*z = zone
	return nil
}

func (z PowerSafeZone) Validate() error {
	if math.IsNaN(z.Latitude) || math.IsInf(z.Latitude, 0) || z.Latitude < -90 || z.Latitude > 90 ||
		math.IsNaN(z.Longitude) || math.IsInf(z.Longitude, 0) || z.Longitude < -180 || z.Longitude > 180 ||
		math.IsNaN(z.RadiusMeters) || math.IsInf(z.RadiusMeters, 0) || z.RadiusMeters < 25 || z.RadiusMeters > 5000 {
		return errors.New("power-safe zone requires valid coordinates and a radius between 25 and 5000 meters")
	}
	return nil
}

type DevicePowerPolicy struct {
	DeviceID      string         `json:"deviceId"`
	VehicleID     string         `json:"vehicleId"`
	PowerSafeZone *PowerSafeZone `json:"powerSafeZone"`
}

func (p *DevicePowerPolicy) UnmarshalJSON(raw []byte) error {
	var fields struct {
		DeviceID      string          `json:"deviceId"`
		VehicleID     string          `json:"vehicleId"`
		PowerSafeZone json.RawMessage `json:"powerSafeZone"`
	}
	if decodePowerPolicyJSON(raw, &fields) != nil || fields.DeviceID == "" || fields.VehicleID == "" || fields.PowerSafeZone == nil {
		return errors.New("power policy requires deviceId, vehicleId and explicit powerSafeZone")
	}
	// An omitted zone must not masquerade as removal and erase an offline cache.
	var zone *PowerSafeZone
	if err := json.Unmarshal(fields.PowerSafeZone, &zone); err != nil {
		return errors.New("invalid power-safe zone")
	}
	*p = DevicePowerPolicy{DeviceID: fields.DeviceID, VehicleID: fields.VehicleID, PowerSafeZone: zone}
	return nil
}

func decodePowerPolicyJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid power policy JSON")
	}
	return nil
}

func (s *Store) DevicePowerPolicy(ctx context.Context, token string) (out DevicePowerPolicy, err error) {
	ctx, done := operation(ctx, "db.device.power_policy")
	defer func() { done(err) }()
	if len(token) != 43 {
		return out, ErrDeviceUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	d, err := readDevice(ctx, tx, "token_hash=? AND revoked=0", deviceTokenHash(token))
	if err != nil {
		return out, err
	}
	return DevicePowerPolicy{DeviceID: d.ID, VehicleID: d.VehicleID, PowerSafeZone: d.PowerSafeZone}, nil
}
