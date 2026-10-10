package smartcar

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func webhookPayloadFixture(t *testing.T, eventType string, change func(map[string]any)) []byte {
	t.Helper()
	stamp := time.Date(2026, 10, 10, 12, 0, 0, 123000000, time.UTC).UnixMilli()
	payload := map[string]any{
		"eventId": "event-fixture-0001", "eventType": eventType,
		"data": map[string]any{
			"user":    map[string]any{"id": "provider-user-fixture"},
			"vehicle": map[string]any{"id": "provider-vehicle-fixture", "mode": "live"},
		},
		"meta": map[string]any{
			"version": "4.0", "webhookId": "webhook-fixture-0001",
			"deliveryId": "delivery-fixture-0001", "deliveredAt": stamp + 60000,
			"mode": "LIVE",
		},
	}
	data := payload["data"].(map[string]any)
	switch eventType {
	case "VEHICLE_STATE":
		data["signals"] = []any{
			map[string]any{"code": "location-preciselocation", "body": map[string]any{"latitude": 0, "longitude": 0, "locationType": "LAST_PARKED"}, "meta": map[string]any{"oemUpdatedAt": stamp, "retrievedAt": stamp + 30000}},
			map[string]any{"code": "internalcombustionengine-fuellevel", "body": map[string]any{"value": 0, "unit": "percent"}, "meta": map[string]any{"oemUpdatedAt": stamp, "retrievedAt": stamp + 30000}},
		}
		payload["meta"].(map[string]any)["sequence"] = stamp + 1000
		payload["meta"].(map[string]any)["signalCount"] = 2
	case "VEHICLE_ERROR":
		data["errors"] = []any{map[string]any{"type": "CONNECTED_SERVICES_ACCOUNT", "code": "AUTHENTICATION_FAILED", "state": "ERROR", "signals": []any{map[string]any{"code": "location-preciselocation"}}}}
	case "VERIFY":
		payload["data"] = map[string]any{"challenge": "challenge-fixture-0001"}
		delete(payload["meta"].(map[string]any), "mode")
	}
	if change != nil {
		change(payload)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWebhookSignatureAndChallenge(t *testing.T) {
	// RFC 4231 test case 2 independently checks the key/message order and hex encoding.
	const message = "what do ya want for nothing?"
	const key = "Jefe"
	const signature = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
	for _, input := range []struct {
		name, payload, signature, key string
		want                          bool
	}{
		{"valid", message, signature, key, true},
		{"upper hex", message, strings.ToUpper(signature), key, true},
		{"whitespace changes raw bytes", message + "\n", signature, key, false},
		{"wrong key", message, signature, "different", false},
		{"empty key", message, signature, "", false},
		{"missing signature", message, "", key, false},
		{"short signature", message, signature[:62], key, false},
		{"prefixed signature", message, "sha256=" + signature, key, false},
		{"nonhex signature", message, strings.Repeat("z", 64), key, false},
	} {
		t.Run(input.name, func(t *testing.T) {
			if got := VerifyWebhookSignature([]byte(input.payload), input.signature, input.key); got != input.want {
				t.Fatalf("signature accepted = %v, want %v", got, input.want)
			}
		})
	}
	got, err := WebhookChallenge("challenge-fixture-0001", key)
	if err != nil || got != "91aebf662463a8afe7feda6f5d87492339225c369a2f489a7d62ca18f821daa3" {
		t.Fatalf("challenge response = %q, %v", got, err)
	}
	for _, challenge := range []string{"", "short", "   ", strings.Repeat("a", 1025), `{"eventType":"VEHICLE_STATE","data":{}}`} {
		if _, err := WebhookChallenge(challenge, key); err == nil {
			t.Fatal("accepted invalid challenge")
		}
	}
	if _, err := WebhookChallenge(message, ""); err == nil {
		t.Fatal("accepted empty management token")
	}
}

func TestWebhookStateMatchesPollingIdentityAndOEMTime(t *testing.T) {
	raw := webhookPayloadFixture(t, "VEHICLE_STATE", nil)
	payload, err := DecodeWebhookPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if payload.UserID != "provider-user-fixture" || payload.VehicleID != "provider-vehicle-fixture" || payload.WebhookID != "webhook-fixture-0001" || payload.Mode != "live" || payload.Sequence == nil {
		t.Fatalf("lost routing information: %+v", payload)
	}
	stamp := "2026-10-10T12:00:00.123Z"
	now := time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC)
	got := adapt(payload.VehicleID, payload.Signals, now, nil)
	want := adapt(payload.VehicleID, []remoteSignal{
		fixtureSignal("location-preciselocation", `{"latitude":0,"longitude":0,"locationType":"LAST_PARKED"}`, stamp),
		fixtureSignal("internalcombustionengine-fuellevel", `{"value":0,"unit":"percent"}`, stamp),
	}, now, nil)
	if !reflect.DeepEqual(got.Batch, want.Batch) {
		t.Fatalf("webhook and polling ingestion differ: got %+v, want %+v", got.Batch, want.Batch)
	}
	if len(got.Batch.Observations) != 1 || len(got.Batch.Contexts) != 1 || got.Batch.Contexts[0].Location.Latitude != 0 || got.Batch.Contexts[0].Location.Type != "LAST_PARKED" {
		t.Fatalf("zero values/location missing: %+v", got)
	}
	if got.Latest.Format(time.RFC3339Nano) != stamp || got.Latest.Equal(payload.DeliveredAt) {
		t.Fatal("delivery/retrieval time replaced OEM time")
	}
	if payload.Signals[0].Meta.IngestedAt != "" {
		t.Fatal("invented provider error timestamp")
	}
}

func TestWebhookStatePartialFailureAndMissingTimestamps(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC)
	for _, missing := range []any{nil, int64(0), int64(946684799999), now.Add(time.Hour).UnixMilli()} {
		raw := webhookPayloadFixture(t, "VEHICLE_STATE", func(p map[string]any) {
			signals := p["data"].(map[string]any)["signals"].([]any)
			signals[0].(map[string]any)["meta"].(map[string]any)["oemUpdatedAt"] = missing
		})
		payload, err := DecodeWebhookPayload(raw)
		if err != nil {
			t.Fatal(err)
		}
		got := adapt(payload.VehicleID, payload.Signals, now, nil)
		if len(got.Batch.Contexts) != 0 || len(got.Batch.Observations) != 1 || got.Unavailable != 1 {
			t.Fatalf("missing/invalid OEM timestamp used a fallback: %+v", got)
		}
	}
	raw := webhookPayloadFixture(t, "VEHICLE_STATE", func(p map[string]any) {
		signal := p["data"].(map[string]any)["signals"].([]any)[0].(map[string]any)
		signal["status"] = map[string]any{"value": "ERROR", "error": map[string]any{"type": "CONNECTED_SERVICES_ACCOUNT", "code": "AUTHENTICATION_FAILED"}}
	})
	payload, err := DecodeWebhookPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	authorizedAt := now.Add(-time.Hour)
	got := adapt(payload.VehicleID, payload.Signals, now, &authorizedAt)
	if len(got.Batch.Contexts) != 0 || len(got.Batch.Observations) != 1 || got.Unavailable != 1 || got.Reconnect {
		t.Fatalf("signal error created data or latched reconnect without an onset timestamp: %+v", got)
	}
	if len(payload.Errors) != 1 || payload.Errors[0].Code != "AUTHENTICATION_FAILED" || payload.Errors[0].State != "ERROR" {
		t.Fatal("lost separate signal error metadata")
	}
}

func TestWebhookErrorAndResolutionNeverInventReadings(t *testing.T) {
	for _, state := range []string{"ERROR", "RESOLVED"} {
		raw := webhookPayloadFixture(t, "VEHICLE_ERROR", func(p map[string]any) {
			p["data"].(map[string]any)["errors"].([]any)[0].(map[string]any)["state"] = state
		})
		payload, err := DecodeWebhookPayload(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload.Signals) != 0 || len(payload.Errors) != 1 || payload.Errors[0].State != state || payload.Errors[0].Signals[0] != "location-preciselocation" {
			t.Fatalf("invalid error normalization: %+v", payload)
		}
		got := adapt(payload.VehicleID, payload.Signals, time.Now(), nil)
		if len(got.Batch.Observations) != 0 || len(got.Batch.Contexts) != 0 || got.Latest != nil || got.Reconnect {
			t.Fatal("error or resolution invented observations or authentication chronology")
		}
	}
}

func TestWebhookReceiptHashIgnoresDeliveryChanges(t *testing.T) {
	first, err := DecodeWebhookPayload(webhookPayloadFixture(t, "VEHICLE_STATE", nil))
	if err != nil {
		t.Fatal(err)
	}
	retryRaw := webhookPayloadFixture(t, "VEHICLE_STATE", func(p map[string]any) {
		meta := p["meta"].(map[string]any)
		meta["deliveredAt"] = meta["deliveredAt"].(int64) + 25000
		meta["deliveryId"] = "delivery-fixture-retry"
	})
	retry, err := DecodeWebhookPayload(retryRaw)
	if err != nil || retry.ContentHash != first.ContentHash || retry.DeliveredAt.Equal(first.DeliveredAt) {
		t.Fatalf("retry changed substantive hash: %v", err)
	}
	changed, err := DecodeWebhookPayload(webhookPayloadFixture(t, "VEHICLE_STATE", func(p map[string]any) {
		p["data"].(map[string]any)["signals"].([]any)[1].(map[string]any)["body"].(map[string]any)["value"] = 50
	}))
	if err != nil || changed.ContentHash == first.ContentHash {
		t.Fatal("conflicting content was not distinguished")
	}
	var reformatted any
	if err := json.Unmarshal(retryRaw, &reformatted); err != nil {
		t.Fatal(err)
	}
	pretty, err := json.MarshalIndent(reformatted, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	spaced, err := DecodeWebhookPayload(pretty)
	if err != nil || spaced.ContentHash != first.ContentHash {
		t.Fatal("JSON whitespace changed substantive hash")
	}
}

func TestWebhookDecodeVerifyAndForwardCompatibility(t *testing.T) {
	payload, err := DecodeWebhookPayload(webhookPayloadFixture(t, "VERIFY", nil))
	if err != nil || payload.EventType != "VERIFY" || payload.Challenge != "challenge-fixture-0001" || payload.UserID != "" || len(payload.Signals) != 0 {
		t.Fatalf("invalid VERIFY normalization: %+v, %v", payload, err)
	}
	// Smartcar's callback verification guide uses RFC3339 metadata, while its
	// event reference uses epoch milliseconds. Neither becomes a measurement.
	verification := []byte(`{"eventId":"52f6e0bb-1369-45da-a61c-9e67d092d6db","eventType":"VERIFY","data":{"challenge":"3a5c8f72-e6d9-4b1a-9f2e-8c7d6a5b4e3f"},"meta":{"version":"4.0","webhookId":"5a8e5e38-1e12-4011-a36d-56f120053f9e","deliveryId":"5d569643-3a47-4cd1-a3ec-db5fc1f6f03b","deliveredAt":"2025-07-31T19:38:42.332Z"}}`)
	verified, err := DecodeWebhookPayload(verification)
	if err != nil || verified.DeliveredAt.Format(time.RFC3339Nano) != "2025-07-31T19:38:42.332Z" {
		t.Fatalf("documented callback payload rejected: %v", err)
	}
	_, err = DecodeWebhookPayload(webhookPayloadFixture(t, "VERIFY", func(p map[string]any) {
		p["data"].(map[string]any)["challenge"] = `{"eventType":"VEHICLE_STATE","data":{}}`
	}))
	if err == nil {
		t.Fatal("unsigned VERIFY could become an event signing oracle")
	}
	payload, err = DecodeWebhookPayload(webhookPayloadFixture(t, "VEHICLE_STATE", func(p map[string]any) {
		p["future"] = map[string]any{"value": true}
		p["data"].(map[string]any)["newField"] = "future"
		p["data"].(map[string]any)["signals"].([]any)[0].(map[string]any)["code"] = "new-unsupported-signal"
		p["meta"].(map[string]any)["mode"] = "SIMULATED"
		p["data"].(map[string]any)["vehicle"].(map[string]any)["mode"] = "simulated"
	}))
	if err != nil || payload.Mode != "simulated" {
		t.Fatalf("future fields or simulated mode rejected: %v", err)
	}
	got := adapt(payload.VehicleID, payload.Signals, time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC), nil)
	if got.Unsupported != 1 || len(got.Batch.Observations) != 1 {
		t.Fatal("unsupported signal blocked valid data")
	}
}

func TestWebhookDecodeRejectsAmbiguousOrMalformedEnvelope(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"wrong version":         func(p map[string]any) { p["meta"].(map[string]any)["version"] = "2.0" },
		"missing user":          func(p map[string]any) { delete(p["data"].(map[string]any), "user") },
		"bad vehicle":           func(p map[string]any) { p["data"].(map[string]any)["vehicle"].(map[string]any)["id"] = "../x" },
		"unknown mode":          func(p map[string]any) { p["meta"].(map[string]any)["mode"] = "unknown" },
		"mode mismatch":         func(p map[string]any) { p["data"].(map[string]any)["vehicle"].(map[string]any)["mode"] = "simulated" },
		"unknown type":          func(p map[string]any) { p["eventType"] = "OTHER" },
		"missing signals":       func(p map[string]any) { delete(p["data"].(map[string]any), "signals") },
		"null signals":          func(p map[string]any) { p["data"].(map[string]any)["signals"] = nil },
		"count mismatch":        func(p map[string]any) { p["meta"].(map[string]any)["signalCount"] = 10 },
		"negative sequence":     func(p map[string]any) { p["meta"].(map[string]any)["sequence"] = -1 },
		"missing delivery time": func(p map[string]any) { delete(p["meta"].(map[string]any), "deliveredAt") },
		"epoch delivery time":   func(p map[string]any) { p["meta"].(map[string]any)["deliveredAt"] = 0 },
		"fractional timestamp": func(p map[string]any) {
			p["data"].(map[string]any)["signals"].([]any)[0].(map[string]any)["meta"].(map[string]any)["oemUpdatedAt"] = 123.4
		},
		"duplicate signal": func(p map[string]any) {
			signals := p["data"].(map[string]any)["signals"].([]any)
			signals[1] = signals[0]
		},
		"oversized signal list": func(p map[string]any) { p["data"].(map[string]any)["signals"] = make([]any, 1001) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeWebhookPayload(webhookPayloadFixture(t, "VEHICLE_STATE", change)); err == nil {
				t.Fatal("accepted malformed payload")
			}
		})
	}
	raw := string(webhookPayloadFixture(t, "VEHICLE_STATE", nil))
	for name, input := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "trailing": raw + `{}`, "oversized": strings.Repeat(" ", MaxWebhookPayloadBytes+1),
		"duplicate envelope key": strings.Replace(raw, `"eventType":"VEHICLE_STATE"`, `"eventType":"VERIFY","eventType":"VEHICLE_STATE"`, 1),
		"duplicate nested key":   strings.Replace(raw, `"latitude":0`, `"latitude":1,"latitude":0`, 1),
		"nested too deep":        `{"data":` + strings.Repeat("[", 102) + `0` + strings.Repeat("]", 102) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeWebhookPayload([]byte(input)); err == nil {
				t.Fatal("accepted ambiguous payload")
			}
		})
	}
}
