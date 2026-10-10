package smartcar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func webhookTestSignature(raw []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func sendWebhookFixture(t *testing.T, s *Service, event map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReceiveWebhook(context.Background(), raw, webhookTestSignature(raw, s.managementToken))
	return err
}

func configureWebhookFixture(s *Service) {
	s.managementToken = "webhook-test-management-token"
	s.webhookID = "11111111-2222-3333-4444-555555555555"
}

func TestWebhookReceiptDoesNotPreventReauthorization(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	configureWebhookFixture(s)
	external := ""
	providerFixture(t, s, &external, nil)
	previous := adoptFixture(t, s, vehicle)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	event := webhookEvent(t, "event-before-reauth", at)
	if err := sendWebhookFixture(t, s, event); err != nil {
		t.Fatal(err)
	}
	begin, err := s.Begin(ctx, vehicle, "reconnect")
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := url.Parse(begin.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	external = authorize.Query().Get("external_id")
	complete, err := s.Complete(ctx, vehicle, begin.SessionID, Completion{State: authorize.Query().Get("state"), UserID: "user-private-fixture", VehicleID: "vehicle-private-fixture", ExternalID: external})
	if err != nil || len(complete.Candidates) != 1 {
		t.Fatal("reauthorization completion failed", err)
	}
	rebound, err := s.Bind(ctx, vehicle, begin.SessionID, complete.Candidates[0].CandidateID)
	if err != nil || rebound.ConnectionID == previous.ConnectionID || rebound.AuthorizationStartedAt == nil {
		t.Fatal("receipt prevented connection generation replacement", err)
	}
	if err := sendWebhookFixture(t, s, event); err != nil {
		t.Fatal("rebound connection rejected idempotent measurements", err)
	}
	exported, err := store.Export(ctx)
	if err != nil || len(exported.Signals) != 1 || len(exported.SignalContexts) != 1 {
		t.Fatal("reauthorization lost or duplicated existing observations", err)
	}
}

func TestWebhookEqualOEMTimestampPreservesMetricsAndSuccessAcrossPoll(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	configureWebhookFixture(s)
	providerFixture(t, s, nil, nil)
	adoptFixture(t, s, vehicle)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	if err := sendWebhookFixture(t, s, webhookEvent(t, "event-initial-metrics", at)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldSuccess := time.Now().Add(-time.Minute).UTC()
	claimed.Status.LastSuccessAt = &oldSuccess
	event := webhookEvent(t, "event-equal-time-metric", at)
	event["data"].(map[string]any)["signals"] = []any{map[string]any{
		"code": "internalcombustionengine-oillife", "body": map[string]any{"value": 70, "unit": "percent"}, "meta": map[string]any{"oemUpdatedAt": at.UnixMilli()},
	}}
	if err := sendWebhookFixture(t, s, event); err != nil {
		t.Fatal(err)
	}
	webhookStatus, err := s.Status(ctx, vehicle)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishSmartcar(ctx, claimed, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, vehicle)
	if err != nil || !slices.Contains(status.SupportedMetrics, "fuel_level_pct") || !slices.Contains(status.SupportedMetrics, "oil_life_pct") || status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(*webhookStatus.LastSuccessAt) || !status.LatestObservedAt.Equal(at) {
		t.Fatal("equal OEM time lost webhook metrics or newer success", status, err)
	}
}

func TestWebhookDoesNotHidePollingApplicationAuthenticationFailure(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	configureWebhookFixture(s)
	providerFixture(t, s, nil, nil)
	adoptFixture(t, s, vehicle)
	ctx := context.Background()
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	if err := sendWebhookFixture(t, s, webhookEvent(t, "event-during-auth-failure", at)); err != nil {
		t.Fatal(err)
	}
	claimed.Status.State, claimed.Status.ErrorCode = "temporary_error", "application_authentication"
	if err := store.FinishSmartcar(ctx, claimed, nil, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, vehicle)
	if err != nil || status.State != "temporary_error" || status.ErrorCode != "application_authentication" || status.LatestObservedAt == nil || !status.LatestObservedAt.Equal(at) || status.LastSuccessAt == nil {
		t.Fatal("webhook hid real polling authentication failure or lost measurements", status, err)
	}
}

func TestWebhookFailedTransactionLeavesNoReceiptAndRetrySucceeds(t *testing.T) {
	s, store, vehicle, filename := serviceFixture(t)
	configureWebhookFixture(s)
	providerFixture(t, s, nil, nil)
	adoptFixture(t, s, vehicle)
	ctx := context.Background()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Fail after measurements and status have been written, proving the receipt
	// and the earlier writes share a rollback boundary.
	if _, err := db.Exec(`CREATE TRIGGER fail_webhook_receipt BEFORE INSERT ON smartcar_webhook_receipts BEGIN SELECT RAISE(ABORT,'synthetic persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	event := webhookEvent(t, "event-retry-after-failure", time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond))
	if err := sendWebhookFixture(t, s, event); err == nil {
		t.Fatal("failed persistence was acknowledged")
	}
	var receipts int
	if err := db.QueryRow(`SELECT count(*) FROM smartcar_webhook_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("failed event left receipt", err)
	}
	exported, err := store.Export(ctx)
	if err != nil || len(exported.Signals) != 0 || len(exported.SignalContexts) != 0 {
		t.Fatal("failed event leaked partial measurements", err)
	}
	status, err := s.Status(ctx, vehicle)
	if err != nil || status.LastWebhookAt != nil || status.LastSuccessAt != nil || status.LatestObservedAt != nil {
		t.Fatal("failed event leaked status", err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_webhook_receipt`); err != nil {
		t.Fatal(err)
	}
	if err := sendWebhookFixture(t, s, event); err != nil {
		t.Fatal("same event could not retry after rollback", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM smartcar_webhook_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("successful retry missing receipt", err)
	}
	exported, err = store.Export(ctx)
	if err != nil || len(exported.Signals) != 1 || len(exported.SignalContexts) != 1 {
		t.Fatal("successful retry missing observations", err)
	}
}

func TestWebhookErrorAndResolutionPreserveDataAndSuccessStatus(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	configureWebhookFixture(s)
	providerFixture(t, s, nil, nil)
	adoptFixture(t, s, vehicle)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	sendError := func(id, state string) {
		t.Helper()
		event := webhookEvent(t, id, at)
		event["eventType"] = "VEHICLE_ERROR"
		delete(event["meta"].(map[string]any), "sequence")
		data := event["data"].(map[string]any)
		delete(data, "signals")
		data["errors"] = []any{map[string]any{"type": "CONNECTED_SERVICES_ACCOUNT", "code": "AUTHENTICATION_FAILED", "state": state, "signals": []any{map[string]any{"code": "location-preciselocation"}}}}
		if err := sendWebhookFixture(t, s, event); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"ERROR", "RESOLVED"} {
		sendError("event-before-data-"+state, state)
		status, err := s.Status(ctx, vehicle)
		if err != nil || status.LastSuccessAt != nil || status.LatestObservedAt != nil || status.State != "provisioning" {
			t.Fatal("error/resolution invented successful vehicle data", status, err)
		}
	}
	if err := sendWebhookFixture(t, s, webhookEvent(t, "event-valid-before-errors", at)); err != nil {
		t.Fatal(err)
	}
	beforeStatus, err := s.Status(ctx, vehicle)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"ERROR", "RESOLVED"} {
		sendError("event-after-data-"+state, state)
		status, err := s.Status(ctx, vehicle)
		if err != nil || !reflect.DeepEqual(status.LastSuccessAt, beforeStatus.LastSuccessAt) || !reflect.DeepEqual(status.LatestObservedAt, beforeStatus.LatestObservedAt) || status.State != beforeStatus.State || status.LastWebhookEventType != "VEHICLE_ERROR" {
			t.Fatal("error/resolution changed data freshness", status, err)
		}
		exported, err := store.Export(ctx)
		if err != nil || !reflect.DeepEqual(exported.Signals, before.Signals) || !reflect.DeepEqual(exported.SignalContexts, before.SignalContexts) {
			t.Fatal("error/resolution changed stored measurements", err)
		}
	}
}

func webhookEvent(t *testing.T, id string, at time.Time) map[string]any {
	t.Helper()
	return map[string]any{
		"eventId": id, "eventType": "VEHICLE_STATE",
		"meta": map[string]any{"version": "4.0", "webhookId": "11111111-2222-3333-4444-555555555555", "deliveryId": "delivery-fixture", "deliveredAt": time.Now().UnixMilli(), "mode": "TEST", "sequence": 1},
		"data": map[string]any{"user": map[string]any{"id": "user-private-fixture"}, "vehicle": map[string]any{"id": "vehicle-private-fixture", "mode": "simulated"}, "signals": []any{
			map[string]any{"code": "location-preciselocation", "body": map[string]any{"latitude": 0, "longitude": 0, "locationType": "LAST_PARKED"}, "meta": map[string]any{"oemUpdatedAt": at.UnixMilli()}},
			map[string]any{"code": "internalcombustionengine-fuellevel", "body": map[string]any{"value": 50, "unit": "percent"}, "meta": map[string]any{"oemUpdatedAt": at.UnixMilli()}},
		}},
	}
}

func TestWebhookDurabilityReplayIsolationAndPollRace(t *testing.T) {
	s, store, vehicle, path := serviceFixture(t)
	s.managementToken = "webhook-test-management-token"
	s.webhookID = "11111111-2222-3333-4444-555555555555"
	providerFixture(t, s, nil, nil)
	adoptFixture(t, s, vehicle)
	ctx := context.Background()
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	protectedDeadline := claimed.NextAttemptAt.Truncate(time.Second)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	event := webhookEvent(t, "event-fixture-1", at)
	send := func(event map[string]any) error {
		t.Helper()
		raw, _ := json.Marshal(event)
		// Event signatures are computed directly; VERIFY deliberately refuses JSON challenges.
		sig := webhookTestSignature(raw, s.managementToken)
		_, err := s.ReceiveWebhook(ctx, raw, sig)
		return err
	}
	if err = send(event); err != nil {
		t.Fatal(err)
	}
	location, err := store.LatestLocation(ctx, vehicle)
	if err != nil || location == nil || !location.RecordedAt.Equal(at) || location.LocationType != "LAST_PARKED" {
		t.Fatal("location lost OEM time", err)
	}
	claimed.Status.State = "provisioning"
	claimed.Status.ErrorCode = "no_timestamped_signals"
	if err = store.FinishSmartcar(ctx, claimed, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	status, _ := s.Status(ctx, vehicle)
	if status.State != "connected" || status.LastWebhookAt == nil || status.LatestObservedAt == nil || !status.NextAttemptAt.Equal(protectedDeadline) {
		t.Fatal("poll race lost webhook status or cadence", status)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := garage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s.store = reopened
	event["meta"].(map[string]any)["deliveryId"] = "delivery-retry-fixture"
	event["meta"].(map[string]any)["deliveredAt"] = time.Now().Add(time.Minute).UnixMilli()
	if err = send(event); err != nil {
		t.Fatal("durable retry failed", err)
	}
	exported, _ := reopened.Export(ctx)
	if len(exported.Signals) != 1 || len(exported.SignalContexts) != 1 {
		t.Fatal("duplicate webhook observations")
	}
	event["data"].(map[string]any)["signals"].([]any)[1].(map[string]any)["body"].(map[string]any)["value"] = 60
	if err = send(event); !errors.Is(err, garage.ErrSignalConflict) {
		t.Fatal("conflicting event ID accepted", err)
	}
	older := webhookEvent(t, "event-fixture-older", at.Add(-time.Hour))
	if err = send(older); err != nil {
		t.Fatal(err)
	}
	latest, _ := reopened.LatestLocation(ctx, vehicle)
	if !latest.RecordedAt.Equal(at) {
		t.Fatal("older delivery replaced current location")
	}
	wrongUser := webhookEvent(t, "event-wrong-user", at)
	wrongUser["data"].(map[string]any)["user"] = map[string]any{"id": "another-private-user"}
	if err = send(wrongUser); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-user delivery accepted")
	}
	if err = s.Detach(ctx, vehicle); err != nil {
		t.Fatal(err)
	}
	if err = send(webhookEvent(t, "event-after-detach", at)); err != nil {
		t.Fatal("detached event should be ignored", err)
	}
	trips, _ := reopened.Trips(ctx, vehicle, 10, time.Time{}, "")
	if len(trips) != 0 {
		t.Fatal("Smartcar snapshot created trips")
	}
}
