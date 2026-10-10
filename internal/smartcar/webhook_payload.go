package smartcar

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
)

const MaxWebhookPayloadBytes = 1 << 20

// WebhookPayload contains only routing, idempotency and ingestion information.
// Delivery time is not an OEM measurement time or an error onset time.
type WebhookPayload struct {
	EventID, EventType, UserID, VehicleID, WebhookID string
	Mode, Challenge, ContentHash                     string
	Sequence                                         *int64
	DeliveredAt                                      time.Time
	Signals                                          []remoteSignal
	Errors                                           []WebhookSignalError
}

type WebhookSignalError struct {
	Type, Code, State string
	Signals           []string
}

type webhookSignal struct {
	Code   string          `json:"code"`
	Body   json.RawMessage `json:"body"`
	Status *struct {
		Value string `json:"value"`
		Error *struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	} `json:"status"`
	Meta struct {
		OEMUpdatedAt *int64 `json:"oemUpdatedAt"`
		RetrievedAt  *int64 `json:"retrievedAt"`
	} `json:"meta"`
}

// DecodeWebhookPayload does not authenticate a delivery. Callers must verify
// the raw signature and match the configured webhook, user and vehicle first.
func DecodeWebhookPayload(raw []byte) (WebhookPayload, error) {
	var out WebhookPayload
	invalid := errors.New("invalid Smartcar webhook payload")
	if len(raw) == 0 || len(raw) > MaxWebhookPayloadBytes || jsonutil.Validate(raw) != nil {
		return out, invalid
	}
	var wire struct {
		EventID   string          `json:"eventId"`
		EventType string          `json:"eventType"`
		Data      json.RawMessage `json:"data"`
		Meta      struct {
			Version     string               `json:"version"`
			WebhookID   string               `json:"webhookId"`
			DeliveryID  string               `json:"deliveryId"`
			DeliveredAt *webhookDeliveryTime `json:"deliveredAt"`
			Mode        string               `json:"mode"`
			Sequence    *int64               `json:"sequence"`
			SignalCount *int                 `json:"signalCount"`
		} `json:"meta"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Meta.Version != "4.0" ||
		!validProviderID(wire.EventID) || !validProviderID(wire.Meta.WebhookID) ||
		!validProviderID(wire.Meta.DeliveryID) || wire.Meta.DeliveredAt == nil {
		return out, invalid
	}
	if wire.Meta.Sequence != nil && *wire.Meta.Sequence < 0 {
		return out, invalid
	}
	var data struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
		Vehicle struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		} `json:"vehicle"`
		Signals []webhookSignal `json:"signals"`
		Errors  []struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			State   string `json:"state"`
			Signals []struct {
				Code string `json:"code"`
			} `json:"signals"`
		} `json:"errors"`
	}
	if json.Unmarshal(wire.Data, &data) != nil {
		return out, invalid
	}
	out = WebhookPayload{EventID: wire.EventID, EventType: wire.EventType,
		WebhookID: wire.Meta.WebhookID, UserID: data.User.ID, VehicleID: data.Vehicle.ID,
		Mode: webhookMode(wire.Meta.Mode), Sequence: wire.Meta.Sequence,
		DeliveredAt: wire.Meta.DeliveredAt.Time}
	if out.EventType == "VERIFY" {
		if !validWebhookChallenge(data.Challenge) {
			return WebhookPayload{}, invalid
		}
		out.Challenge = data.Challenge
	} else {
		if !validProviderID(out.UserID) || !validProviderID(out.VehicleID) ||
			(out.Mode != "live" && out.Mode != "simulated") ||
			(data.Vehicle.Mode != "" && webhookMode(data.Vehicle.Mode) != out.Mode) {
			return WebhookPayload{}, invalid
		}
		switch out.EventType {
		case "VEHICLE_STATE":
			if data.Signals == nil || len(data.Signals) > 1000 ||
				(wire.Meta.SignalCount != nil && *wire.Meta.SignalCount != len(data.Signals)) {
				return WebhookPayload{}, invalid
			}
			seen := map[string]bool{}
			for _, signal := range data.Signals {
				if !validWebhookCode(signal.Code) || seen[signal.Code] {
					return WebhookPayload{}, invalid
				}
				seen[signal.Code] = true
				var normalized remoteSignal
				normalized.Attributes.Code = signal.Code
				normalized.Attributes.Body = signal.Body
				normalized.Meta.OEMUpdatedAt = webhookTimestamp(signal.Meta.OEMUpdatedAt)
				normalized.Meta.RetrievedAt = webhookTimestamp(signal.Meta.RetrievedAt)
				if signal.Status != nil {
					if signal.Status.Value != "ERROR" && signal.Status.Value != "SUCCESS" {
						return WebhookPayload{}, invalid
					}
					normalized.Attributes.Status.Value = signal.Status.Value
					if signal.Status.Value == "ERROR" && signal.Status.Error != nil {
						failure := signal.Status.Error
						if (failure.Type != "" && !validWebhookCode(failure.Type)) || (failure.Code != "" && !validWebhookCode(failure.Code)) {
							return WebhookPayload{}, invalid
						}
						// Webhook errors have no onset timestamp. Keep them separate
						// so adapt cannot mistake a cached error for failed new consent.
						out.Errors = append(out.Errors, WebhookSignalError{Type: failure.Type, Code: failure.Code, State: "ERROR", Signals: []string{signal.Code}})
					}
				} else if len(signal.Body) > 0 && !bytes.Equal(signal.Body, []byte("null")) {
					normalized.Attributes.Status.Value = "SUCCESS"
				}
				out.Signals = append(out.Signals, normalized)
			}
		case "VEHICLE_ERROR":
			if len(data.Errors) == 0 || len(data.Errors) > 1000 {
				return WebhookPayload{}, invalid
			}
			signalCount := 0
			for _, failure := range data.Errors {
				if !validWebhookCode(failure.Type) || (failure.Code != "" && !validWebhookCode(failure.Code)) ||
					(failure.State != "ERROR" && failure.State != "RESOLVED") {
					return WebhookPayload{}, invalid
				}
				normalized := WebhookSignalError{Type: failure.Type, Code: failure.Code, State: failure.State}
				for _, signal := range failure.Signals {
					signalCount++
					if signalCount > 1000 || !validWebhookCode(signal.Code) {
						return WebhookPayload{}, invalid
					}
					normalized.Signals = append(normalized.Signals, signal.Code)
				}
				out.Errors = append(out.Errors, normalized)
			}
		default:
			return WebhookPayload{}, invalid
		}
	}
	// Keep exact JSON numbers while canonicalizing object keys; retries change
	// delivery metadata but must not change the event's substantive contents.
	var canonicalData any
	decoder := json.NewDecoder(bytes.NewReader(wire.Data))
	decoder.UseNumber()
	if decoder.Decode(&canonicalData) != nil {
		return WebhookPayload{}, invalid
	}
	canonical, err := json.Marshal(struct {
		EventType, WebhookID, Version, Mode string
		Sequence                            *int64
		Data                                any
	}{out.EventType, out.WebhookID, wire.Meta.Version, out.Mode, out.Sequence, canonicalData})
	if err != nil {
		return WebhookPayload{}, invalid
	}
	out.ContentHash = fingerprint(string(canonical))
	return out, nil
}

func webhookTimestamp(milliseconds *int64) string {
	if milliseconds == nil {
		return ""
	}
	at := time.UnixMilli(*milliseconds).UTC()
	if at.Year() < 2000 || at.Year() > 9999 {
		return ""
	}
	return at.Format(time.RFC3339Nano)
}

func webhookMode(mode string) string {
	mode = strings.ToLower(mode)
	if mode == "test" {
		return "simulated"
	}
	return mode
}

func validWebhookCode(code string) bool {
	if len(code) == 0 || len(code) > 128 {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validWebhookChallenge(challenge string) bool {
	if len(challenge) < 16 || len(challenge) > 1024 {
		return false
	}
	// VERIFY is unsigned and shares the event signing key. Restrict it to
	// opaque tokens so the endpoint cannot sign attacker-chosen JSON events.
	for _, c := range challenge {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_./+=", c)) {
			return false
		}
	}
	return true
}

type webhookDeliveryTime struct{ time.Time }

func (at *webhookDeliveryTime) UnmarshalJSON(raw []byte) error {
	invalid := errors.New("invalid Smartcar webhook delivery time")
	if len(raw) > 0 && raw[0] == '"' {
		var stamp string
		if json.Unmarshal(raw, &stamp) != nil {
			return invalid
		}
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return invalid
		}
		at.Time = parsed.UTC()
	} else {
		var milliseconds int64
		if json.Unmarshal(raw, &milliseconds) != nil {
			return invalid
		}
		at.Time = time.UnixMilli(milliseconds).UTC()
	}
	if at.Year() < 2000 || at.Year() > 9999 {
		return invalid
	}
	return nil
}

func VerifyWebhookSignature(raw []byte, signature, managementToken string) bool {
	if managementToken == "" || len(signature) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(managementToken))
	_, _ = mac.Write(raw)
	return hmac.Equal(mac.Sum(nil), decoded)
}

func WebhookChallenge(challenge, managementToken string) (string, error) {
	if managementToken == "" || !validWebhookChallenge(challenge) {
		return "", errors.New("invalid Smartcar webhook challenge")
	}
	mac := hmac.New(sha256.New, []byte(managementToken))
	_, _ = mac.Write([]byte(challenge))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
