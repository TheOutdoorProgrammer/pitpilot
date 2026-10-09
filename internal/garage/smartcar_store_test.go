package garage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func smartcarStoreFixture(t *testing.T) (*Store, SmartcarConnection) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "garage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	v := Vehicle{ID: NewID(), Name: "Synthetic", CreatedAt: time.Now().UTC()}
	if err = s.CreateVehicle(ctx, v); err != nil {
		t.Fatal(err)
	}
	session := SmartcarSession{ID: NewID(), VehicleID: v.ID, ExpiresAt: time.Now().Add(time.Hour), Encrypted: []byte("synthetic-encrypted-session")}
	if err = s.CreateSmartcarSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	c := SmartcarConnection{ID: NewID(), VehicleID: v.ID, RemoteKey: "synthetic-hmac", Encrypted: []byte("synthetic-encrypted-identity"), NextAttemptAt: time.Now().Add(-time.Minute), Status: SmartcarStatus{State: "provisioning", SupportedMetrics: []string{}}}
	if err = s.BindSmartcar(ctx, session, "", c); err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestSmartcarConcurrentLeaseAndAtomicSignalFailure(t *testing.T) {
	s, c := smartcarStoreFixture(t)
	ctx := context.Background()
	var winners atomic.Int32
	var wg sync.WaitGroup
	var claimed SmartcarConnection
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			v, err := s.ClaimSmartcar(ctx, time.Now(), time.Hour)
			if err == nil {
				if winners.Add(1) == 1 {
					claimed = v
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("lease winners %d", winners.Load())
	}
	at := time.Now().UTC()
	bad := SignalBatch{Source: "smartcar", BatchID: "invalid", Observations: []SignalObservation{{Key: "first", Metric: "fuel_level_pct", Unit: "%", Statistic: "sample", Quality: "measured", Value: 10, ObservedAt: &at}, {Key: "second", Metric: "fuel_level_pct", Unit: "%", Statistic: "sample", Quality: "measured", Value: 101, ObservedAt: &at}}}
	claimed.Status.State = "connected"
	claimed.NextAttemptAt = at.Add(time.Hour)
	if err := s.FinishSmartcar(ctx, claimed, &bad, time.Time{}); err == nil {
		t.Fatal("invalid signal batch accepted")
	}
	export, err := s.Export(ctx)
	if err != nil || len(export.Signals) != 0 {
		t.Fatal("partial signal write escaped rollback")
	}
	unchanged, err := s.SmartcarConnection(ctx, c.VehicleID)
	if err != nil || unchanged.Status.State != "provisioning" {
		t.Fatal("status committed before signals")
	}
	if err = s.DetachSmartcar(ctx, c.VehicleID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishSmartcar(ctx, claimed, nil, time.Time{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("detached lease accepted")
	}
}

func TestSmartcarSessionCASAndExpiry(t *testing.T) {
	s, c := smartcarStoreFixture(t)
	ctx := context.Background()
	v := SmartcarSession{ID: NewID(), VehicleID: c.VehicleID, ExpiresAt: time.Now().Add(time.Minute), Encrypted: []byte("encrypted")}
	if err := s.CreateSmartcarSession(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSmartcarSession(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSmartcarSession(ctx, v); !errors.Is(err, ErrSmartcarConflict) {
		t.Fatal("stale session update accepted")
	}
	if _, err := s.db.Exec(`UPDATE smartcar_sessions SET expires_at=0 WHERE id=?`, v.ID); err != nil {
		t.Fatal(err)
	}
	v.Version = 1
	if err := s.UpdateSmartcarSession(ctx, v); !errors.Is(err, ErrSmartcarConflict) {
		t.Fatal("expired session update accepted")
	}
}

func TestSmartcarCrashAndRebindCannotBypassHourlyDeadline(t *testing.T) {
	s, c := smartcarStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	claimed, err := s.ClaimSmartcar(ctx, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RequestSmartcarSync(ctx, c.VehicleID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimSmartcar(ctx, now.Add(3*time.Minute), time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired lease allowed another request before an hour", err)
	}
	claimed.Status.State = "reconnect_required"
	claimed.RetryAt = now.Add(3 * time.Hour)
	claimed.NextAttemptAt = claimed.RetryAt
	if err = s.FinishSmartcar(ctx, claimed, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	session := SmartcarSession{ID: NewID(), VehicleID: c.VehicleID, ExpiresAt: now.Add(time.Hour), Encrypted: []byte("fixture")}
	if err = s.CreateSmartcarSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	c.ID = NewID()
	c.NextAttemptAt = now
	if err = s.BindSmartcar(ctx, session, claimed.ID, c); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimSmartcar(ctx, now.Add(2*time.Hour), time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reauthorization erased provider backoff", err)
	}
	if _, err = s.ClaimSmartcar(ctx, now.Add(3*time.Hour+time.Second), time.Hour); err != nil {
		t.Fatal("rebound connection could not recover", err)
	}
}
