package picollector

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
)

type LegacyConfig struct {
	Endpoint  string `json:"endpoint"`
	DeviceID  string `json:"deviceId"`
	TokenFile string `json:"tokenFile"`
}

var legacyIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var legacyDTC = regexp.MustCompile(`^[PBCU][0-3][0-9A-F]{3}$`)

type LegacyEvent struct {
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"id"`
	DeviceID      string             `json:"device_id"`
	BootID        string             `json:"boot_id"`
	Sequence      uint64             `json:"sequence"`
	UptimeMS      int64              `json:"uptime_ms"`
	ObservedAt    *time.Time         `json:"observed_at"`
	Readings      map[string]float64 `json:"readings"`
	DTCs          []string           `json:"dtcs"`
	PendingDTCs   []string           `json:"pending_dtcs,omitzero"`
	PermanentDTCs []string           `json:"permanent_dtcs,omitzero"`
}

func (e LegacyEvent) Validate() error {
	if e.SchemaVersion != 1 || !legacyIdentifier.MatchString(e.ID) || !legacyIdentifier.MatchString(e.DeviceID) || !legacyIdentifier.MatchString(e.BootID) || e.Sequence == 0 || e.UptimeMS < 0 {
		return errors.New("invalid legacy event identity")
	}
	if e.ObservedAt != nil && (e.ObservedAt.Year() < 2020 || e.ObservedAt.Year() > 2100 || e.ObservedAt.After(time.Now().Add(5*time.Minute))) {
		return errors.New("invalid legacy observation time")
	}
	if len(e.Readings) == 0 && e.DTCs == nil && e.PendingDTCs == nil && e.PermanentDTCs == nil {
		return errors.New("empty legacy observation")
	}
	for name, value := range e.Readings {
		if !obd.ValidLegacyReading(name, value) {
			return errors.New("invalid legacy reading")
		}
	}
	for _, codes := range [][]string{e.DTCs, e.PendingDTCs, e.PermanentDTCs} {
		if len(codes) > 127 {
			return errors.New("oversized legacy diagnostics")
		}
		for _, code := range codes {
			if !legacyDTC.MatchString(code) {
				return errors.New("invalid legacy diagnostic")
			}
		}
	}
	return nil
}

func legacyEvent(c *LegacyConfig, sample obd.Observation, at time.Time, id, boot string, uptime int64) *LegacyEvent {
	if c == nil {
		return nil
	}
	return &LegacyEvent{SchemaVersion: 1, ID: id, DeviceID: c.DeviceID, BootID: boot, UptimeMS: uptime, ObservedAt: &at, Readings: sample.Readings, DTCs: sample.DTCs, PendingDTCs: sample.PendingDTCs, PermanentDTCs: sample.PermanentDTCs}
}
func NewLegacyClient(c Config) (*Client, error) {
	if c.Legacy == nil {
		return nil, nil
	}
	token, err := PrivateToken(c.Legacy.TokenFile)
	if err != nil {
		return nil, err
	}
	u, err := endpoint(c.Legacy.Endpoint, c.AllowLoopbackHTTP)
	if err != nil {
		return nil, err
	}
	u.Path = ""
	return NewClient(u.String(), token, c.AllowLoopbackHTTP)
}
func (c *Client) UploadLegacy(ctx context.Context, event LegacyEvent) error {
	var ack struct {
		Accepted []string `json:"accepted"`
	}
	if err := c.request(ctx, "POST", "/v1/obd/events", struct {
		Events []LegacyEvent `json:"events"`
	}{[]LegacyEvent{event}}, &ack, 200); err != nil {
		return err
	}
	if len(ack.Accepted) != 1 || ack.Accepted[0] != event.ID {
		return errors.New("incomplete legacy acknowledgement")
	}
	return nil
}
func uploadLegacy(ctx context.Context, q *Queue, c *Client) {
	delay := time.Second
	for ctx.Err() == nil {
		p, ok, err := q.FirstLegacy()
		if err != nil {
			return
		}
		if !ok {
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if p.Legacy == nil {
			return
		}
		err = diag.Operation(ctx, "collector.legacy_delivery", func(ctx context.Context) error {
			if err := c.UploadLegacy(ctx, *p.Legacy); err != nil {
				return err
			}
			if err := diag.Operation(ctx, "queue.legacy_ack", func(context.Context) error { return q.AckLegacy(p) }); err != nil {
				return err
			}
			slog.InfoContext(ctx, "delivery acknowledged", "destination", "legacy")
			return nil
		})
		if err == nil {
			delay = time.Second
			continue
		}
		if !wait(ctx, delay) {
			return
		}
		delay = min(delay*2, 5*time.Minute)
	}
}
