package smartcar

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func serviceFixture(t *testing.T) (*Service, *garage.Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "garage.db")
	store, err := garage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	v := garage.Vehicle{ID: garage.NewID(), Name: "Fixture", CreatedAt: time.Now().UTC()}
	if err = store.CreateVehicle(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	s, err := New(store, testConfig(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.client.token = "fixture-token"
	s.client.expires = time.Now().Add(time.Hour)
	return s, store, v.ID, path
}
func testConfig() Config {
	return Config{ApplicationID: "11111111-2222-3333-4444-555555555555", ClientID: "client-fixture", ClientSecret: "fixture-secret-32-characters-long", Mode: "simulated", EncryptionKey: bytes.Repeat([]byte{7}, 32), PollInterval: time.Hour}
}
func providerFixture(t *testing.T, s *Service, external *string, signalFunc func(http.ResponseWriter, *http.Request)) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/connections" {
			e := ""
			if external != nil {
				e = *external
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []remoteConnection{fixtureRemote("vehicle-private-fixture", "user-private-fixture", e)}, "meta": map[string]int{"totalCount": 1, "pageNumber": 1}})
			return
		}
		if r.Method != "GET" {
			t.Error("provider mutation attempted")
		}
		if signalFunc != nil {
			signalFunc(w, r)
			return
		}
		t.Error("unexpected provider call")
		w.WriteHeader(500)
	}))
	t.Cleanup(server.Close)
	s.client.apiURL = server.URL
}
func adoptFixture(t *testing.T, s *Service, vehicle string) garage.SmartcarStatus {
	t.Helper()
	session, err := s.Adopt(context.Background(), vehicle, "user-private-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Candidates) != 1 {
		t.Fatal("missing candidate")
	}
	result, err := s.Bind(context.Background(), vehicle, session.SessionID, session.Candidates[0].CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConnectStateSelectionReplayAndEncryptedStorage(t *testing.T) {
	s, store, vehicle, path := serviceFixture(t)
	ctx := context.Background()
	external := ""
	providerFixture(t, s, &external, nil)
	begin, err := s.Begin(ctx, vehicle, "connect")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(begin.AuthorizationURL)
	q := u.Query()
	external = q.Get("external_id")
	if q.Get("application_id") != testConfig().ApplicationID || q.Get("response_type") != "none" || q.Get("client_id") != "" || strings.Contains(q.Get("scope"), "control_") {
		t.Fatal("unsafe connect URL")
	}
	input := Completion{State: "wrong", UserID: "user-private-fixture", ExternalID: external}
	if _, err = s.Complete(ctx, vehicle, begin.SessionID, input); !errors.Is(err, ErrSession) {
		t.Fatal("wrong state accepted")
	}
	input.State = q.Get("state")
	input.ExternalID = "wrong"
	if _, err = s.Complete(ctx, vehicle, begin.SessionID, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("external ID mismatch accepted")
	}
	input.ExternalID = external
	complete, err := s.Complete(ctx, vehicle, begin.SessionID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Complete(ctx, vehicle, begin.SessionID, input); !errors.Is(err, ErrSession) {
		t.Fatal("callback replay accepted")
	}
	if _, err = s.Bind(ctx, vehicle, begin.SessionID, "not-a-candidate"); !errors.Is(err, ErrInvalid) {
		t.Fatal("arbitrary candidate accepted")
	}
	status, err := s.Bind(ctx, vehicle, begin.SessionID, complete.Candidates[0].CandidateID)
	if err != nil || status.State != "provisioning" || status.AuthorizationStartedAt == nil {
		t.Fatalf("bind %v", err)
	}
	if _, err = s.Bind(ctx, vehicle, begin.SessionID, complete.Candidates[0].CandidateID); !errors.Is(err, ErrSession) {
		t.Fatal("bind replay accepted")
	}
	v, err := store.SmartcarConnection(ctx, vehicle)
	if err != nil {
		t.Fatal(err)
	}
	var identityValue identity
	if err = s.open("connection", v.ID, v.Encrypted, &identityValue); err != nil || identityValue.UserID != "user-private-fixture" {
		t.Fatal("encrypted identity roundtrip")
	}
	if s.open("connection", "other-row", v.Encrypted, &identityValue) == nil {
		t.Fatal("ciphertext can be transplanted")
	}
	export, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(export)
	if bytes.Contains(raw, []byte("user-private-fixture")) || bytes.Contains(raw, []byte(status.ConnectionID)) {
		t.Fatal("integration secrets entered export")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"user-private-fixture", "vehicle-private-fixture", external} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private identity stored in plaintext")
		}
	}
	restored, err := garage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = New(restored, testConfig(), s.logger); err != nil {
		t.Fatal("correct key failed after restore", err)
	}
	wrong := testConfig()
	wrong.EncryptionKey = bytes.Repeat([]byte{8}, 32)
	if _, err = New(restored, wrong, s.logger); err == nil {
		t.Fatal("wrong restore key accepted")
	}
}

func TestAdoptionUniqueBindingAndCancelledReconnect(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	ctx := context.Background()
	providerFixture(t, s, nil, nil)
	before := adoptFixture(t, s, vehicle)
	other := garage.Vehicle{ID: garage.NewID(), Name: "Other", CreatedAt: time.Now().UTC()}
	if err := store.CreateVehicle(ctx, other); err != nil {
		t.Fatal(err)
	}
	adoption, err := s.Adopt(ctx, other.ID, "user-private-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Bind(ctx, other.ID, adoption.SessionID, adoption.Candidates[0].CandidateID); !errors.Is(err, garage.ErrSmartcarConflict) {
		t.Fatal("same remote vehicle bound twice", err)
	}
	reconnect, err := s.Begin(ctx, vehicle, "reconnect")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(reconnect.AuthorizationURL)
	if u.Path != "/oauth/reauthenticate" || u.Query().Get("response_type") != "vehicle_id" {
		t.Fatal("wrong reconnect contract")
	}
	if result, err := s.Complete(ctx, vehicle, reconnect.SessionID, Completion{State: u.Query().Get("state"), Error: "access_denied"}); err != nil || result.State != "cancelled" {
		t.Fatal("cancellation not handled")
	}
	after, err := s.Status(ctx, vehicle)
	if err != nil || after.ConnectionID != before.ConnectionID {
		t.Fatal("cancelled reconnect destroyed binding")
	}
	if err = s.Detach(ctx, vehicle); err != nil {
		t.Fatal(err)
	}
	if status, err := s.Status(ctx, vehicle); err != nil || status.State != "disconnected" {
		t.Fatal("detach failed")
	}
}

func TestWorkerIdempotencyOutOfOrderAndDetachLease(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	value := `{"value":30,"unit":"percent"}`
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []remoteSignal{fixtureSignal("internalcombustionengine-fuellevel", value, at.Format(time.RFC3339Nano))}, "meta": map[string]int{"totalCount": 1}})
	})
	adoptFixture(t, s, vehicle)
	run := func() {
		t.Helper()
		claimed, err := store.ClaimSmartcar(ctx, time.Now().Add(2*time.Hour), s.interval)
		if err != nil {
			t.Fatal(err)
		}
		s.process(ctx, claimed)
	}
	run()
	run()
	export, err := store.Export(ctx)
	if err != nil || len(export.Signals) != 1 {
		t.Fatal("repeat produced duplicates", err, len(export.Signals))
	}
	newest := at
	at = at.Add(-time.Hour)
	value = `{"value":20,"unit":"percent"}`
	run()
	status, err := s.Status(ctx, vehicle)
	if err != nil || status.LatestObservedAt == nil || !status.LatestObservedAt.Equal(newest) {
		t.Fatal("out of order observation regressed latest")
	}
	export, err = store.Export(ctx)
	if err != nil || len(export.Signals) != 2 {
		t.Fatal("older history was lost")
	}
	claimed, err := store.ClaimSmartcar(ctx, time.Now().Add(2*time.Hour), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Detach(ctx, vehicle); err != nil {
		t.Fatal(err)
	}
	at = at.Add(-time.Hour)
	s.process(ctx, claimed)
	export, _ = store.Export(ctx)
	if len(export.Signals) != 2 {
		t.Fatal("detached in-flight job wrote data")
	}
}

func TestWorkerRetryDeadlineSurvivesRestartAndManualSync(t *testing.T) {
	s, store, vehicle, path := serviceFixture(t)
	ctx := context.Background()
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"errors":[{"code":"VEHICLE"}]}`))
	})
	adoptFixture(t, s, vehicle)
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	s.process(ctx, claimed)
	status, err := s.Sync(ctx, vehicle)
	if err != nil || status.NextAttemptAt == nil || status.NextAttemptAt.Before(time.Now().Add(59*time.Minute)) {
		t.Fatal("manual refresh bypassed backoff", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := garage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.ClaimSmartcar(ctx, time.Now().Add(30*time.Minute), s.interval); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restart lost persistent deadline", err)
	}
	if _, err = restored.ClaimSmartcar(ctx, time.Now().Add(2*time.Hour), s.interval); err != nil {
		t.Fatal("deadline never expires", err)
	}
}

func TestClaimCooldownSurvivesCrashAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		cooldown time.Duration
	}{
		{"configured six hours", 6 * time.Hour, 6 * time.Hour},
		{"minimum hour enforced", time.Minute, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, vehicle, path := serviceFixture(t)
			ctx := context.Background()
			providerFixture(t, s, nil, nil)
			adoptFixture(t, s, vehicle)
			now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
			if _, err := store.ClaimSmartcar(ctx, now, tc.interval); err != nil {
				t.Fatal(err)
			}
			// No finish: simulate losing the worker after its durable claim.
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := garage.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			connection, err := restored.SmartcarConnection(ctx, vehicle)
			if err != nil || !connection.NextAttemptAt.Equal(now.Add(tc.cooldown)) {
				t.Fatalf("configured cooldown was not persisted: %v %v", connection.NextAttemptAt, err)
			}
			before := now.Add(tc.cooldown - time.Minute)
			if err := restored.RequestSmartcarSync(ctx, vehicle, before); err != nil {
				t.Fatal(err)
			}
			// Even a shorter interval after restart must preserve the existing deadline.
			if _, err := restored.ClaimSmartcar(ctx, before, time.Hour); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("restart or manual sync bypassed persisted cooldown", err)
			}
			if _, err := restored.ClaimSmartcar(ctx, now.Add(tc.cooldown+time.Second), time.Hour); err != nil {
				t.Fatal("expired cooldown did not permit recovery", err)
			}
		})
	}
}

func TestWorkerMissingTimestampIsUnavailableNotNow(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []remoteSignal{fixtureSignal("odometer-traveleddistance", `{"value":1000,"unit":"km"}`, "")}, "meta": map[string]int{"totalCount": 1}})
	})
	adoptFixture(t, s, vehicle)
	claimed, err := store.ClaimSmartcar(context.Background(), time.Now(), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	s.process(context.Background(), claimed)
	status, err := s.Status(context.Background(), vehicle)
	if err != nil || status.UnavailableSignals != 1 || status.LatestObservedAt != nil || status.ErrorCode != "no_timestamped_signals" {
		t.Fatalf("missing timestamp %+v %v", status, err)
	}
	export, _ := store.Export(context.Background())
	if len(export.Signals) != 0 {
		t.Fatal("timestamp manufactured")
	}
}

func TestWorkerObservationConflictPreservesPreviousSuccess(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	value := 10
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{"value": value, "unit": "percent"})
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []remoteSignal{fixtureSignal("internalcombustionengine-fuellevel", string(body), at)}, "meta": map[string]int{"totalCount": 1}})
	})
	adoptFixture(t, s, vehicle)
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	s.process(ctx, claimed)
	before, err := s.Status(ctx, vehicle)
	if err != nil {
		t.Fatal(err)
	}
	value = 11
	claimed, err = store.ClaimSmartcar(ctx, time.Now().Add(2*time.Hour), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	s.process(ctx, claimed)
	after, err := s.Status(ctx, vehicle)
	if err != nil || after.ErrorCode != "observation_conflict" || after.LastSuccessAt == nil || !after.LastSuccessAt.Equal(*before.LastSuccessAt) {
		t.Fatalf("conflict claimed success %+v %v", after, err)
	}
	export, _ := store.Export(ctx)
	if len(export.Signals) != 1 || export.Signals[0].Observation.Value != 10 {
		t.Fatal("conflict overwrote history")
	}
}

func TestAppRateLimitDefersOtherConnections(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	ctx := context.Background()
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"errors":[{"code":"SMARTCAR_API"}]}`))
	})
	adoptFixture(t, s, vehicle)
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	s.process(ctx, claimed)
	other := garage.Vehicle{ID: garage.NewID(), Name: "Other synthetic", CreatedAt: time.Now().UTC()}
	if err = store.CreateVehicle(ctx, other); err != nil {
		t.Fatal(err)
	}
	session := garage.SmartcarSession{ID: garage.NewID(), VehicleID: other.ID, ExpiresAt: time.Now().Add(time.Hour), Encrypted: []byte("fixture")}
	if err = store.CreateSmartcarSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	connection := garage.SmartcarConnection{ID: garage.NewID(), VehicleID: other.ID, RemoteKey: "other-private-hmac", Encrypted: []byte("fixture"), NextAttemptAt: time.Now().Add(-time.Hour), Status: garage.SmartcarStatus{State: "provisioning", SupportedMetrics: []string{}}}
	if err = store.BindSmartcar(ctx, session, "", connection); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimSmartcar(ctx, time.Now().Add(30*time.Minute), s.interval); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("application backoff did not protect other connection", err)
	}
}
