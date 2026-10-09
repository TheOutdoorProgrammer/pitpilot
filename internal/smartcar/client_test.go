package smartcar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestApplicationTokenConcurrentExpiryAndUnauthorizedRetry(t *testing.T) {
	var tokens atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.Method != "POST" {
				t.Error("non-POST token")
			}
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" {
				t.Error("wrong grant")
			}
			tokens.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"synthetic-access-token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-access-token" {
			t.Error("missing token")
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	c := newClient("synthetic-client", "synthetic-secret")
	c.tokenURL = server.URL + "/token"
	c.apiURL = server.URL
	var out any
	if err := c.get(context.Background(), "test", "/resource", "", &out); err != nil {
		t.Fatal(err)
	}
	if tokens.Load() != 2 {
		t.Fatal("401 must reacquire once")
	}
	c.mu.Lock()
	c.expires = time.Now().Add(30 * time.Second)
	c.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			var value any
			if err := c.get(context.Background(), "test", "/resource", "", &value); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if tokens.Load() != 3 {
		t.Fatalf("concurrent token reacquisition: %d", tokens.Load())
	}
}

func TestClientRateLimitAndResponsePrivacy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1800")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"errors":[{"code":"SMARTCAR_API","detail":"private-vin-and-account"}]}`))
	}))
	defer server.Close()
	c := newClient("synthetic-client", "synthetic-secret")
	c.apiURL = server.URL
	c.token = "synthetic-token"
	c.expires = time.Now().Add(time.Hour)
	var out any
	err := c.get(context.Background(), "signals", "/private-vehicle", "private-user", &out)
	var p *providerError
	if !errors.As(err, &p) || p.Code != "rate_limited" || !p.AppWide || p.RetryAt.Before(time.Now().Add(29*time.Minute)) {
		t.Fatalf("unexpected rate handling %v", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("private provider details escaped")
	}
}

func TestConnectionsPaginationFiltersAndOwnership(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("filter[userId]") != "user-fixture" || q.Get("filter[user.externalId]") != "external-fixture" || q.Get("filter[vehicle.mode]") != "simulated" {
			t.Error("incorrect filters")
		}
		v := fixtureRemote("vehicle-fixture-"+q.Get("page[number]"), "user-fixture", "external-fixture")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []remoteConnection{v}, "meta": map[string]int{"totalCount": 2, "pageNumber": calls}})
	}))
	defer server.Close()
	c := newClient("synthetic-client", "synthetic-secret")
	c.apiURL = server.URL
	c.token = "synthetic-token"
	c.expires = time.Now().Add(time.Hour)
	result, err := c.connections(context.Background(), "user-fixture", "external-fixture", "simulated")
	if err != nil || len(result) != 2 || calls != 2 {
		t.Fatalf("pagination %d %d %v", len(result), calls, err)
	}
}

func TestSignalAdapterPresenceUnitsTimestampAndStableIdentity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	stamp := now.Add(-time.Hour).Format(time.RFC3339Nano)
	good := fixtureSignal("internalcombustionengine-fuellevel", `{"value":0,"unit":"percent"}`, stamp)
	input := []remoteSignal{good, fixtureSignal("internalcombustionengine-range", `{"value":200,"unit":"km"}`, stamp), fixtureSignal("location-preciselocation", `{"latitude":0,"longitude":0,"locationType":"LAST_PARKED"}`, stamp)}
	r := adapt("vehicle-fixture", input, now, nil)
	if len(r.Batch.Observations) != 2 || len(r.Batch.Contexts) != 1 || r.Unavailable != 0 || r.Batch.Validate() != nil {
		t.Fatalf("valid signals %+v", r)
	}
	for _, o := range r.Batch.Observations {
		if o.Metric == "range_km" && o.Quality != "estimated" {
			t.Fatal("range claimed measured")
		}
	}
	input[0].Meta.RetrievedAt = now.Format(time.RFC3339Nano)
	again := adapt("vehicle-fixture", input, now.Add(time.Minute), nil)
	if again.Batch.BatchID != r.Batch.BatchID {
		t.Fatal("retrieval time changed immutable observation")
	}
	for _, tc := range []struct{ code, body, at string }{{"internalcombustionengine-fuellevel", `{"unit":"percent"}`, stamp}, {"internalcombustionengine-fuellevel", `{"value":null,"unit":"percent"}`, stamp}, {"internalcombustionengine-fuellevel", `{"value":0,"value":50,"unit":"percent"}`, stamp}, {"internalcombustionengine-fuellevel", `{"value":0,"unit":"fraction"}`, stamp}, {"internalcombustionengine-fuellevel", `{"value":101,"unit":"percent"}`, stamp}, {"internalcombustionengine-fuellevel", `{"value":10,"unit":"percent"}`, ""}, {"location-preciselocation", `{"locationType":"LAST_PARKED"}`, stamp}, {"wheel-tires", `{"rowCount":2,"columnCount":2,"unit":"kPa","values":[{"tirePressure":220}]}`, stamp}} {
		got := adapt("vehicle-fixture", []remoteSignal{fixtureSignal(tc.code, tc.body, tc.at)}, now, nil)
		if got.Unavailable != 1 || len(got.Batch.Observations) != 0 || len(got.Batch.Contexts) != 0 {
			t.Fatalf("invalid fields manufactured observation for %s %s", tc.code, tc.body)
		}
	}
}

func TestSignalAdapterTirePositionsAndPartialErrors(t *testing.T) {
	now := time.Now().UTC()
	tire := fixtureSignal("wheel-tires", `{"rowCount":2,"columnCount":2,"unit":"kPa","values":[{"row":0,"column":0,"tirePressure":0},{"row":1,"column":1,"tirePressure":220}]}`, now.Format(time.RFC3339Nano))
	bad := fixtureSignal("odometer-traveleddistance", `{}`, "")
	bad.Attributes.Status.Value = "ERROR"
	r := adapt("vehicle-fixture", []remoteSignal{tire, bad}, now, nil)
	if len(r.Batch.Observations) != 2 || r.Unavailable != 1 {
		t.Fatalf("partial values %+v", r)
	}
	if r.Batch.Observations[0].Metric == r.Batch.Observations[1].Metric {
		t.Fatal("wheel positions collapsed")
	}
}

func TestProviderAuthenticationFailureDoesNotIngestCachedBody(t *testing.T) {
	var signal remoteSignal
	if err := json.Unmarshal([]byte(`{"id":"synthetic-signal","attributes":{"code":"internalcombustionengine-fuellevel","status":{"value":"ERROR","error":{"type":"CONNECTED_SERVICES_ACCOUNT","code":"AUTHENTICATION_FAILED"}},"body":{"value":25,"unit":"percent"}},"meta":{"ingestedAt":"2026-10-08T12:00:00Z"}}`), &signal); err != nil {
		t.Fatal(err)
	}
	result := adapt("synthetic-vehicle", []remoteSignal{signal}, time.Now(), nil)
	if !result.Reconnect || result.Unavailable != 1 || len(result.Batch.Observations) != 0 || result.Latest != nil {
		t.Fatal("failed cached provider signal manufactured fresh measurement")
	}
}

func TestAuthenticationErrorsRespectLatestAuthorization(t *testing.T) {
	now := time.Now().UTC()
	authorized := now.Add(-time.Minute)
	for _, tc := range []struct {
		name, ingested string
		reconnect      bool
	}{
		{"cached before consent", now.Add(-7 * time.Hour).Format(time.RFC3339Nano), false},
		{"after consent", now.Format(time.RFC3339Nano), true},
		{"missing time", "", true},
		{"invalid time", "invalid", true},
		{"zero time", "0001-01-01T00:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var signal remoteSignal
			if err := json.Unmarshal([]byte(`{"attributes":{"code":"internalcombustionengine-fuellevel","status":{"value":"ERROR","error":{"code":"AUTHENTICATION_FAILED"}},"body":{"value":25,"unit":"percent"}}}`), &signal); err != nil {
				t.Fatal(err)
			}
			signal.Meta.IngestedAt = tc.ingested
			result := adapt("fixture", []remoteSignal{signal}, now, &authorized)
			if result.Reconnect != tc.reconnect || result.Unavailable != 1 || result.Latest != nil || len(result.Batch.Observations) != 0 {
				t.Fatalf("unexpected adaptation %+v", result)
			}
		})
	}
}

func fixtureSignal(code, body, at string) remoteSignal {
	var v remoteSignal
	v.ID = "signal-fixture"
	v.Attributes.Code = code
	v.Attributes.Status.Value = "SUCCESS"
	v.Attributes.Body = json.RawMessage(body)
	v.Meta.OEMUpdatedAt = at
	return v
}
func fixtureRemote(vehicle, user, external string) remoteConnection {
	var v remoteConnection
	v.ID = "connection-" + vehicle
	v.Attributes.User.ID = user
	v.Attributes.User.ExternalID = external
	v.Attributes.Vehicle.Make = "Synthetic"
	v.Attributes.Vehicle.Model = "Fixture"
	v.Attributes.Vehicle.Year = 2025
	v.Attributes.Vehicle.Mode = "simulated"
	v.Relationships.Vehicle.Data.ID = vehicle
	return v
}
