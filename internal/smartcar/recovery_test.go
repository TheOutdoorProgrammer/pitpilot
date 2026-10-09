package smartcar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestReconnectContinuesHourlyChecksAndRecovers(t *testing.T) {
	s, store, vehicle, _ := serviceFixture(t)
	ctx := context.Background()
	recovered := false
	requests := 0
	providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !recovered {
			_, _ = w.Write([]byte(`{"data":[{"attributes":{"code":"internalcombustionengine-fuellevel","status":{"value":"ERROR","error":{"code":"AUTHENTICATION_FAILED"}}}}],"meta":{"totalCount":1}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []remoteSignal{fixtureSignal("internalcombustionengine-fuellevel", `{"value":30,"unit":"percent"}`, time.Now().UTC().Format(time.RFC3339Nano))}, "meta": map[string]int{"totalCount": 1}})
	})
	adoptFixture(t, s, vehicle)
	claimed, err := store.ClaimSmartcar(ctx, time.Now(), s.interval)
	if err != nil {
		t.Fatal(err)
	}
	s.process(ctx, claimed)
	status, err := s.Sync(ctx, vehicle)
	if err != nil || status.State != "reconnect_required" || status.NextAttemptAt == nil || status.LastSuccessAt != nil {
		t.Fatalf("reconnect lost recovery schedule: %+v %v", status, err)
	}
	if _, err = store.ClaimSmartcar(ctx, status.LastAttemptAt.Add(59*time.Minute), s.interval); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("manual sync bypassed hourly floor", err)
	}
	recovered = true
	claimed, err = store.ClaimSmartcar(ctx, status.LastAttemptAt.Add(time.Hour+time.Second), s.interval)
	if err != nil {
		t.Fatal("reconnect state permanently stopped polling", err)
	}
	s.process(ctx, claimed)
	status, err = s.Status(ctx, vehicle)
	if err != nil || status.State != "connected" || status.LatestObservedAt == nil || status.LastSuccessAt == nil || requests != 2 {
		t.Fatalf("did not recover: %+v requests=%d error=%v", status, requests, err)
	}
}

func TestFailureChecksHonorHourlyFloorAndLongerProviderDelay(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		retry   string
		minimum time.Duration
	}{
		{"server failure", 500, "", time.Hour},
		{"short throttle", 429, "60", time.Hour},
		{"long throttle", 429, "10800", 3 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, vehicle, _ := serviceFixture(t)
			ctx := context.Background()
			providerFixture(t, s, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", tc.retry)
				w.WriteHeader(tc.code)
			})
			adoptFixture(t, s, vehicle)
			claimed, err := store.ClaimSmartcar(ctx, time.Now(), s.interval)
			if err != nil {
				t.Fatal(err)
			}
			s.process(ctx, claimed)
			status, err := s.Sync(ctx, vehicle)
			if err != nil || status.NextAttemptAt == nil || status.NextAttemptAt.Before(status.LastAttemptAt.Add(tc.minimum-time.Second)) {
				t.Fatalf("retry deadline %+v %v", status, err)
			}
		})
	}
}
