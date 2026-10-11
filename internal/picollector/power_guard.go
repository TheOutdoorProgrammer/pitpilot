package picollector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/gps"
)

const powerFixLifetime = 30 * time.Second

type powerGuard struct {
	sync.Mutex
	zone      *garage.PowerSafeZone
	inside    bool
	lastFix   time.Time
	available chan struct{}
	entered   chan struct{}
}

func newPowerGuard(zone *garage.PowerSafeZone) *powerGuard {
	g := &powerGuard{available: make(chan struct{}, 1), entered: make(chan struct{}, 1)}
	g.configure(zone)
	return g
}

func (g *powerGuard) configure(zone *garage.PowerSafeZone) {
	g.Lock()
	defer g.Unlock()
	if zone == nil {
		g.zone = nil
	} else {
		copy := *zone
		g.zone = &copy
	}
	g.inside = false
	g.lastFix = time.Time{}
}

func (g *powerGuard) configuredZone() *garage.PowerSafeZone {
	g.Lock()
	defer g.Unlock()
	if g.zone == nil {
		return nil
	}
	copy := *g.zone
	return &copy
}

func (g *powerGuard) priority(now time.Time) bool {
	if g == nil {
		return false
	}
	g.Lock()
	defer g.Unlock()
	age := now.Sub(g.lastFix)
	return g.zone != nil && g.inside && age >= 0 && age <= powerFixLifetime
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (g *powerGuard) pending() {
	if g.priority(time.Now()) {
		signal(g.available)
	}
}

func (g *powerGuard) observe(ctx context.Context, q *Queue, fix gps.Fix, now time.Time) {
	if g == nil || now.Sub(fix.RecordedAt) < 0 || now.Sub(fix.RecordedAt) > powerFixLifetime {
		return
	}
	g.Lock()
	if g.zone == nil || !fix.RecordedAt.After(g.lastFix) {
		g.Unlock()
		return
	}
	distance := garage.GPSDistanceMeters(garage.Point{Latitude: g.zone.Latitude, Longitude: g.zone.Longitude}, garage.Point{Latitude: fix.Latitude, Longitude: fix.Longitude})
	wasInside := g.inside && now.Sub(g.lastFix) <= powerFixLifetime
	radius := g.zone.RadiusMeters
	if wasInside {
		// A small exit margin prevents stationary GPS jitter from repeatedly
		// waking the uploader and issuing an extra disk barrier.
		radius += 25
	}
	g.inside = distance <= radius
	g.lastFix = fix.RecordedAt
	inside := g.inside
	g.Unlock()
	if inside != wasInside {
		_ = diag.Operation(ctx, "collector.power_guard.transition", func(ctx context.Context) error {
			value := "outside"
			if inside {
				value = "inside"
			}
			slog.InfoContext(ctx, "power guard state changed", "state", value)
			if inside {
				if err := q.Sync(); err != nil {
					slog.ErrorContext(ctx, "power guard queue sync failed")
					return errors.New("queue sync failed")
				}
			}
			return nil
		})
		if inside {
			signal(g.entered)
		}
	}
	g.pending()
}

func (g *powerGuard) waitForUpload(ctx context.Context, delay time.Duration, idle, rateLimited bool) bool {
	if g == nil || rateLimited {
		return wait(ctx, delay)
	}
	wake := g.entered
	if idle {
		wake = g.available
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case <-wake:
			if g.priority(time.Now()) {
				return true
			}
		}
	}
}

func readPowerPolicy(dir string, id Identity) *garage.PowerSafeZone {
	path := filepath.Join(dir, "power-policy.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil
	}
	raw, err := os.ReadFile(path)
	var policy garage.DevicePowerPolicy
	if err != nil || strictJSON(raw, &policy) != nil || policy.DeviceID != id.DeviceID || policy.VehicleID != id.VehicleID || !validPowerZone(policy.PowerSafeZone) {
		return nil
	}
	return policy.PowerSafeZone
}

func validPowerZone(zone *garage.PowerSafeZone) bool {
	return zone == nil || zone.Validate() == nil
}

func equalPowerZones(a, b *garage.PowerSafeZone) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func refreshPowerPolicy(ctx context.Context, dir string, id Identity, client *Client, state *runtimeState) {
	for ctx.Err() == nil {
		delay := 15 * time.Second
		var policy garage.DevicePowerPolicy
		err := client.request(ctx, "GET", "/api/v1/device/power-policy", nil, &policy, 200)
		if err == nil && policy.DeviceID == id.DeviceID && policy.VehicleID == id.VehicleID && validPowerZone(policy.PowerSafeZone) {
			cached := readPowerPolicy(dir, id)
			if !equalPowerZones(cached, policy.PowerSafeZone) || !equalPowerZones(state.power.configuredZone(), policy.PowerSafeZone) {
				raw, _ := json.Marshal(policy)
				if AtomicFile(filepath.Join(dir, "power-policy.json"), raw, 0600) == nil {
					state.power.configure(policy.PowerSafeZone)
				} else {
					slog.ErrorContext(ctx, "power guard policy could not be persisted")
				}
			}
		} else if err != nil {
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && (httpErr.Status == 401 || httpErr.Status == 403) {
				state.pause()
				return
			}
			if httpErr != nil {
				delay = max(delay, httpErr.RetryAfter)
			}
		}
		if !wait(ctx, delay) {
			return
		}
	}
}
