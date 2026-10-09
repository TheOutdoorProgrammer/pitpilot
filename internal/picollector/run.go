package picollector

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	randv2 "math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
)

type Health struct {
	Version    string    `json:"version"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
	CheckedAt  time.Time `json:"checkedAt"`
	QueueReady bool      `json:"queueReady"`
}
type runtimeState struct {
	sync.Mutex
	collection string
	paused     bool
}

func (s *runtimeState) state() (string, bool) {
	s.Lock()
	defer s.Unlock()
	return s.collection, s.paused
}
func (s *runtimeState) set(v string) { s.Lock(); s.collection = v; s.Unlock() }
func (s *runtimeState) pause()       { s.Lock(); s.paused = true; s.collection = "paused"; s.Unlock() }

type Sampler interface {
	SampleDetails(context.Context) (obd.Observation, error)
	Close() error
}
type OpenSampler func(context.Context) (Sampler, error)

func Run(ctx context.Context, c Config, version string, open OpenSampler) error {
	id, err := ReadIdentity(c.StateDirectory)
	if err != nil {
		return err
	}
	legacyID := ""
	if c.Legacy != nil {
		legacyID = c.Legacy.DeviceID
	}
	q, err := OpenQueue(c.StateDirectory, id.DeviceID, c.QueueLimit, legacyID)
	if err != nil {
		return errors.New("queue unavailable")
	}
	defer q.Close()
	legacyClient, err := NewLegacyClient(c)
	if err != nil {
		return err
	}
	client, err := NewClient(c.Server, id.Token, c.AllowLoopbackHTTP)
	if err != nil {
		return err
	}
	if open == nil {
		open = func(ctx context.Context) (Sampler, error) { return obd.Open(ctx, c.SerialPort, c.Baud) }
	}
	state := &runtimeState{collection: "starting"}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); collect(ctx, c, q, state, open) }()
	go func() { defer wg.Done(); upload(ctx, q, client, state) }()
	if legacyClient != nil {
		wg.Add(1)
		go func() { defer wg.Done(); uploadLegacy(ctx, q, legacyClient) }()
	}
	defer wg.Wait()
	started := time.Now().UTC()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		count, rejected, err := q.Status()
		if err != nil {
			cancel()
			return errors.New("queue health failed")
		}
		h := Health{version, os.Getpid(), started, time.Now().UTC(), true}
		raw, _ := json.Marshal(h)
		if AtomicFile(filepath.Join(c.StateDirectory, "health.json"), raw, 0600) != nil {
			cancel()
			return errors.New("health state unavailable")
		}
		cs, _ := state.state()
		us := "idle"
		if raw, err := os.ReadFile("/var/lib/pitpilot-updater/status.json"); err == nil {
			var s struct {
				State string `json:"state"`
			}
			if strictJSON(raw, &s) == nil {
				switch s.State {
				case "idle", "checking", "downloading", "staged", "applying", "healthy", "rolled_back", "failed", "disabled":
					us = s.State
				}
			}
		}
		observed, uploaded, timeErr := q.Times()
		if timeErr != nil {
			cancel()
			return errors.New("queue status unavailable")
		}
		_ = client.Heartbeat(ctx, Heartbeat{Version: version, QueuedBatches: count, CollectionState: cs, UpdateState: us, RejectedSamples: rejected, LastObservedAt: observed, LastUploadAt: uploaded})
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func clockReady(marker string) bool {
	info, err := os.Stat(marker)
	return err == nil && info.Mode().IsRegular() && time.Now().Year() >= 2026
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func collect(ctx context.Context, c Config, q *Queue, state *runtimeState, open OpenSampler) {
	bootID := rand.Text()
	bootAt := time.Now()
	var device Sampler
	defer func() {
		if device != nil {
			_ = device.Close()
		}
	}()
	for ctx.Err() == nil {
		_, paused := state.state()
		if paused {
			state.set("paused")
			return
		}
		if !clockReady(c.TimeSyncMarker) {
			state.set("waiting_clock")
			if !wait(ctx, time.Duration(c.PollSeconds)*time.Second) {
				return
			}
			continue
		}
		started := time.Now()
		operation, cancel := context.WithTimeout(ctx, 45*time.Second)
		var sample obd.Observation
		err := diag.Operation(operation, "obd.sample", func(ctx context.Context) error {
			var err error
			if device == nil {
				candidate, err := open(ctx)
				if err != nil {
					return err
				}
				if candidate == nil {
					return errors.New("adapter unavailable")
				}
				// A failed open may return a typed nil; only retain a successfully opened device.
				device = candidate
			}
			sample, err = device.SampleDetails(ctx)
			if errors.Is(err, obd.ErrECUUnavailable) {
				return nil
			}
			return err
		})
		cancel()
		if err != nil {
			state.set("adapter_unavailable")
			if device != nil {
				_ = device.Close()
				device = nil
			}
		} else {
			// A clock step during the serial read invalidates its wall-clock timestamp.
			drift := time.Now().UTC().Sub(started.UTC()) - time.Since(started)
			if clockReady(c.TimeSyncMarker) && drift < 2*time.Second && drift > -2*time.Second {
				b, err := Batch(sample, started.UTC(), rand.Text())
				if err == nil {
					err = diag.Operation(ctx, "queue.append", func(context.Context) error {
						return q.AppendDelivery(b, legacyEvent(c.Legacy, sample, started.UTC(), b.BatchID, bootID, started.Sub(bootAt).Milliseconds()))
					})
				}
				switch {
				case errors.Is(err, ErrQueueFull):
					state.set("queue_full")
				case err != nil:
					state.set("adapter_unavailable")
					slog.WarnContext(ctx, "sample rejected")
				default:
					state.set("collecting")
				}
			} else {
				state.set("waiting_clock")
			}
		}
		if !wait(ctx, time.Duration(c.PollSeconds)*time.Second) {
			return
		}
	}
}

func Batch(sample obd.Observation, at time.Time, id string) (garage.SignalBatch, error) {
	b := garage.SignalBatch{Source: "pi", BatchID: id}
	if len(sample.Readings) == 0 && sample.DTCs == nil && sample.PendingDTCs == nil && sample.PermanentDTCs == nil {
		return b, errors.New("empty adapter observation")
	}
	units := map[string]string{}
	for _, d := range garage.SignalDefinitions() {
		units[d.Metric] = d.Unit
	}
	keys := make([]string, 0, len(sample.Readings))
	for k := range sample.Readings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o := garage.SignalObservation{Key: id + ":" + k, Metric: k, Unit: units[k], Statistic: "sample", Quality: "measured", Value: sample.Readings[k], ObservedAt: &at}
		if err := o.Validate(); err != nil {
			return b, err
		}
		b.Observations = append(b.Observations, o)
	}
	for _, d := range []struct {
		class string
		codes []string
	}{{"stored", sample.DTCs}, {"pending", sample.PendingDTCs}, {"permanent", sample.PermanentDTCs}} {
		reads := 1
		if d.codes == nil {
			reads = 0
		}
		b.Contexts = append(b.Contexts, garage.SignalContext{Key: id + ":" + d.class, Kind: "diagnostic", ObservedAt: &at, Diagnostic: &garage.SignalDiagnostic{Class: d.class, SuccessfulReads: reads, Unknown: d.codes == nil, Codes: d.codes}})
	}
	return b, b.Validate()
}
func upload(ctx context.Context, q *Queue, c *Client, state *runtimeState) {
	delay := time.Second
	for ctx.Err() == nil {
		p, ok, err := q.First()
		if err != nil {
			slog.ErrorContext(ctx, "queue read failed")
			return
		}
		if !ok {
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		err = diag.Operation(ctx, "collector.delivery", func(ctx context.Context) error {
			if err := c.Upload(ctx, p.Batch); err != nil {
				return err
			}
			if err := diag.Operation(ctx, "queue.ack", func(context.Context) error { return q.Ack(p) }); err != nil {
				return err
			}
			slog.InfoContext(ctx, "delivery acknowledged", "destination", "pitpilot")
			return nil
		})
		if err == nil {
			delay = time.Second
			continue
		}
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && (httpErr.Status == 401 || httpErr.Status == 403) {
			state.pause()
			slog.WarnContext(ctx, "device authorization revoked or invalid; queue retained")
			return
		}
		sleep := delay + time.Duration(randv2.Int64N(int64(delay/4+1)))
		if httpErr != nil && httpErr.RetryAfter > sleep {
			sleep = httpErr.RetryAfter
		}
		if !wait(ctx, sleep) {
			return
		}
		delay = min(delay*2, 5*time.Minute)
	}
}
