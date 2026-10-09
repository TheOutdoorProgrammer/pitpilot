package receiverhistory

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
)

var Identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var diagnosticCode = regexp.MustCompile(`^[PBCU][0-3][0-9A-F]{3}$`)
var ErrInvalid = errors.New("invalid receiver history; source was not imported")

// Event is the retained receiver wire format, including unknown-clock observations.
type Event struct {
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

func (e Event) Validate() error {
	if e.SchemaVersion != 1 || !Identifier.MatchString(e.ID) || !Identifier.MatchString(e.DeviceID) || !Identifier.MatchString(e.BootID) || e.Sequence == 0 || e.UptimeMS < 0 {
		return ErrInvalid
	}
	if e.ObservedAt != nil && (e.ObservedAt.Year() < 2020 || e.ObservedAt.Year() > 2100 || e.ObservedAt.After(time.Now().Add(5*time.Minute))) {
		return ErrInvalid
	}
	if len(e.Readings) == 0 && e.DTCs == nil && e.PendingDTCs == nil && e.PermanentDTCs == nil {
		return ErrInvalid
	}
	for name, value := range e.Readings {
		if !obd.ValidLegacyReading(name, value) {
			return ErrInvalid
		}
	}
	for _, codes := range [][]string{e.DTCs, e.PendingDTCs, e.PermanentDTCs} {
		if len(codes) > 127 {
			return ErrInvalid
		}
		for _, code := range codes {
			if !diagnosticCode.MatchString(code) {
				return ErrInvalid
			}
		}
	}
	return nil
}

func Parse(raw json.RawMessage) (Event, error) {
	var e Event
	if len(raw) == 0 || len(raw) > 64<<10 || jsonutil.Validate(raw) != nil {
		return e, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return e, ErrInvalid
	}
	for key := range fields {
		switch key {
		case "schema_version", "id", "device_id", "boot_id", "sequence", "uptime_ms", "observed_at", "readings", "dtcs", "pending_dtcs", "permanent_dtcs":
		default:
			return e, ErrInvalid
		}
	}
	for _, key := range []string{"schema_version", "id", "device_id", "boot_id", "sequence", "uptime_ms", "observed_at", "readings", "dtcs"} {
		value, exists := fields[key]
		if !exists || (key != "observed_at" && key != "readings" && key != "dtcs" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return e, ErrInvalid
		}
	}
	var readings map[string]json.RawMessage
	if json.Unmarshal(fields["readings"], &readings) != nil {
		return e, ErrInvalid
	}
	for _, value := range readings {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return e, ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&e) != nil || e.Validate() != nil {
		return Event{}, ErrInvalid
	}
	return e, nil
}
