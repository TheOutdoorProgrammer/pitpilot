package picollector

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
)

type LegacyConfig struct {
	Endpoint  string `json:"endpoint"`
	DeviceID  string `json:"deviceId"`
	TokenFile string `json:"tokenFile"`
}

var legacyIdentifier = receiverhistory.Identifier

type LegacyEvent = receiverhistory.Event

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
