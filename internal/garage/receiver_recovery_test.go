package garage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
)

func receiverFixture(t *testing.T) (*Store, ReceiverRecoveryRequest, receiverhistory.Event) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "garage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	at := time.Date(2026, 9, 24, 22, 6, 45, 687965828, time.UTC)
	v := Vehicle{ID: "vehicle", Name: "Receiver fixture", OdometerMiles: 1000, CreatedAt: at}
	if err = s.CreateVehicle(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	e := receiverhistory.Event{SchemaVersion: 1, ID: "event", DeviceID: "receiver", BootID: "boot", Sequence: 22, UptimeMS: 123, ObservedAt: &at, Readings: map[string]float64{"speed_kph": 25, "manifold_kpa": 80}, DTCs: []string{}}
	return s, receiverRequest(t, v.ID, e), e
}
func receiverRequest(t *testing.T, vid string, events ...receiverhistory.Event) ReceiverRecoveryRequest {
	t.Helper()
	r := ReceiverRecoveryRequest{VehicleID: vid, Snapshot: receiverhistory.Snapshot{SHA256: strings.Repeat("a", 64), DeviceID: "receiver"}}
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		r.Snapshot.Events = append(r.Snapshot.Events, raw)
	}
	return r
}
func receiverExport(t *testing.T, s *Store) Export {
	t.Helper()
	v, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v.ExportedAt = time.Time{}
	return v
}
func receiverApply(t *testing.T, s *Store, r ReceiverRecoveryRequest) ReceiverRecoveryReport {
	t.Helper()
	ctx := context.Background()
	preview, err := s.RecoverReceiver(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewToken = preview.PreviewToken
	report, err := s.RecoverReceiver(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied {
		t.Fatal("apply did not commit")
	}
	return report
}

func TestReceiverRecoveryPreviewApplyArchiveAndRepeat(t *testing.T) {
	s, r, event := receiverFixture(t)
	unknown := event
	unknown.ID = "unknown"
	unknown.Sequence++
	unknown.ObservedAt = nil
	r = receiverRequest(t, r.VehicleID, event, unknown, event)
	ctx := context.Background()
	before := receiverExport(t, s)
	preview, err := s.RecoverReceiver(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Events != 2 || preview.Duplicates != 1 || preview.Archived != 2 || preview.UnknownClock != 1 || preview.SamplesCreated != 2 || preview.ContextsCreated != 3 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if !reflect.DeepEqual(before, receiverExport(t, s)) {
		t.Fatal("preview changed target")
	}
	r.PreviewToken = preview.PreviewToken
	applied, err := s.RecoverReceiver(ctx, r)
	if err != nil || !applied.Applied {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	after := receiverExport(t, s)
	if len(after.ReceiverEventArchives) != 2 || len(after.Signals) != 2 || len(after.SignalContexts) != 3 {
		t.Fatal("missing archive/projection")
	}
	for _, archive := range after.ReceiverEventArchives {
		if archive.BackupSHA256 != r.Snapshot.SHA256 {
			t.Fatal("lost backup provenance")
		}
		var raw receiverhistory.Event
		if err = json.Unmarshal(archive.Raw, &raw); err != nil {
			t.Fatal(err)
		}
		if raw.ID == "unknown" && (raw.ObservedAt != nil || len(archive.Projection.Observations) > 0 || len(archive.Projection.Contexts) > 0) {
			t.Fatal("invented unknown event clock")
		}
		for _, o := range archive.Projection.Observations {
			if o.ObservedAt == nil || !o.ObservedAt.Equal(*event.ObservedAt) || o.Quality != "measured" || o.Statistic != "sample" {
				t.Fatal("lost native semantics or nanoseconds")
			}
			if o.Metric == "manifold_kpa" && o.Unit != "kPa" {
				t.Fatal("wrong pressure unit")
			}
		}
	}
	if !reflect.DeepEqual(before.Vehicles, after.Vehicles) || !reflect.DeepEqual(before.Records, after.Records) || !reflect.DeepEqual(before.Trips, after.Trips) || !reflect.DeepEqual(before.OdometerBaselines, after.OdometerBaselines) {
		t.Fatal("recovery changed domain history")
	}
	r.PreviewToken = ""
	repeat := receiverApply(t, s, r)
	if repeat.Skipped != 2 || repeat.Archived != 0 || repeat.SamplesCreated != 0 {
		t.Fatalf("repeat: %+v", repeat)
	}
	if !reflect.DeepEqual(after, receiverExport(t, s)) {
		t.Fatal("repeat changed retained data")
	}
}

func TestReceiverRecoveryExactNativeOverlapAndRoundedTimestamp(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			s, r, event := receiverFixture(t)
			batch, err := MeasuredOBDBatch(event.Readings, event.DTCs, event.PendingDTCs, event.PermanentDTCs, event.ObservedAt.Add(offset), "native-hashed-id")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.IngestSignals(context.Background(), r.VehicleID, batch); err != nil {
				t.Fatal(err)
			}
			before := receiverExport(t, s)
			report := receiverApply(t, s, r)
			after := receiverExport(t, s)
			if offset == 0 {
				if report.SamplesReused != 2 || report.ContextsReused != 3 || report.SamplesCreated != 0 || !reflect.DeepEqual(before.Signals, after.Signals) || !reflect.DeepEqual(before.SignalContexts, after.SignalContexts) {
					t.Fatal("exact native overlap duplicated or changed")
				}
			} else if report.SamplesCreated != 2 || report.SamplesReused != 0 || len(after.Signals) != 4 {
				t.Fatal("different timestamps were merged")
			}
		})
	}
}

func TestReceiverRecoveryRejectsConflictsAtomically(t *testing.T) {
	for _, kind := range []string{"duplicate-id", "sequence", "bad-late-event", "native-value", "stale-preview", "missing-replayed-row", "modified-replayed-row", "source-change", "other-vehicle", "archive-projection", "archive-sequence"} {
		t.Run(kind, func(t *testing.T) {
			s, r, event := receiverFixture(t)
			ctx := context.Background()
			switch kind {
			case "duplicate-id":
				other := event
				other.UptimeMS++
				r = receiverRequest(t, r.VehicleID, event, other)
			case "sequence":
				other := event
				other.ID = "other"
				r = receiverRequest(t, r.VehicleID, event, other)
			case "bad-late-event":
				r.Snapshot.Events = append(r.Snapshot.Events, json.RawMessage(`{"bad":true}`))
			case "native-value":
				batch, _ := MeasuredOBDBatch(map[string]float64{"speed_kph": 99}, event.DTCs, nil, nil, *event.ObservedAt, "native")
				if _, err := s.IngestSignals(ctx, r.VehicleID, batch); err != nil {
					t.Fatal(err)
				}
			case "stale-preview":
				preview, err := s.RecoverReceiver(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				r.PreviewToken = preview.PreviewToken
				batch, _ := MeasuredOBDBatch(map[string]float64{"speed_kph": 1}, nil, nil, nil, event.ObservedAt.Add(time.Second), "later")
				if _, err = s.IngestSignals(ctx, r.VehicleID, batch); err != nil {
					t.Fatal(err)
				}
			case "missing-replayed-row", "modified-replayed-row", "source-change", "other-vehicle", "archive-projection", "archive-sequence":
				receiverApply(t, s, r)
				switch kind {
				case "archive-projection":
					if _, err := s.db.Exec("UPDATE receiver_event_archives SET projection=json_set(projection,'$.observations',json('[]'))"); err != nil {
						t.Fatal(err)
					}
				case "archive-sequence":
					if _, err := s.db.Exec("UPDATE receiver_event_archives SET sequence='999'"); err != nil {
						t.Fatal(err)
					}
				case "missing-replayed-row":
					if _, err := s.db.Exec("DELETE FROM signals WHERE metric='speed_kph'"); err != nil {
						t.Fatal(err)
					}
				case "modified-replayed-row":
					if _, err := s.db.Exec("UPDATE signals SET data=json_set(data,'$.value',999) WHERE metric='speed_kph'"); err != nil {
						t.Fatal(err)
					}
				case "source-change":
					event.UptimeMS++
					r = receiverRequest(t, r.VehicleID, event)
				case "other-vehicle":
					if err := s.CreateVehicle(ctx, Vehicle{ID: "other", Name: "Other", CreatedAt: *event.ObservedAt}); err != nil {
						t.Fatal(err)
					}
					r.VehicleID = "other"
				}
			}
			before := receiverExport(t, s)
			if _, err := s.RecoverReceiver(ctx, r); err == nil {
				t.Fatal("accepted invalid or conflicting history")
			}
			if !reflect.DeepEqual(before, receiverExport(t, s)) {
				t.Fatal("partial mutation on failure")
			}
		})
	}
}

func TestReceiverRecoveryRollbackAfterEarlierInsertAndMissingVehicle(t *testing.T) {
	s, r, event := receiverFixture(t)
	later := event
	later.ID = "z-conflict"
	later.Sequence++
	at := event.ObservedAt.Add(time.Second)
	later.ObservedAt = &at
	batch, _ := MeasuredOBDBatch(map[string]float64{"speed_kph": 99}, nil, nil, nil, at, "existing")
	if _, err := s.IngestSignals(context.Background(), r.VehicleID, batch); err != nil {
		t.Fatal(err)
	}
	r = receiverRequest(t, r.VehicleID, event, later)
	before := receiverExport(t, s)
	if _, err := s.RecoverReceiver(context.Background(), r); !errors.Is(err, ErrReceiverConflict) {
		t.Fatalf("want conflict: %v", err)
	}
	if !reflect.DeepEqual(before, receiverExport(t, s)) {
		t.Fatal("earlier event not rolled back")
	}
	r.VehicleID = "absent"
	if _, err := s.RecoverReceiver(context.Background(), r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found: %v", err)
	}
}

func TestReceiverRecoverySchemaReopenRetainsArchive(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "reopen.db")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 24, 22, 6, 45, 0, time.UTC)
	if err = s.CreateVehicle(context.Background(), Vehicle{ID: "vehicle", Name: "Fixture", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	e := receiverhistory.Event{SchemaVersion: 1, ID: "unknown", DeviceID: "receiver", BootID: "boot", Sequence: 1, Readings: map[string]float64{"speed_kph": 25}}
	receiverApply(t, s, receiverRequest(t, "vehicle", e))
	before := receiverExport(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 8 {
		t.Fatalf("schema: %d %v", version, err)
	}
	if !reflect.DeepEqual(before, receiverExport(t, s)) {
		t.Fatal("reopen changed archive")
	}
}
