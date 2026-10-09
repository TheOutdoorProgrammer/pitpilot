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

func discardFixture(t *testing.T) (*Store, ReceiverRecoveryRequest, ImportBatch) {
	t.Helper()
	s, changedImport, _ := refreshFixture(t)
	before := receiverExport(t, s)
	vehicle := before.ConvertedSignalNotes[0].VehicleID
	at := time.Date(2026, 9, 24, 22, 6, 45, 687965828, time.UTC)
	event := receiverhistory.Event{SchemaVersion: 1, ID: "shared", DeviceID: "receiver", BootID: "boot", Sequence: 1, ObservedAt: &at, Readings: map[string]float64{"speed_kph": 25, "manifold_kpa": 80}, DTCs: []string{}}
	native, err := MeasuredOBDBatch(event.Readings, event.DTCs, nil, nil, at, "native-original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestSignals(context.Background(), vehicle, native); err != nil {
		t.Fatal(err)
	}
	later := event
	later.ID = "created"
	later.Sequence++
	later.Readings = map[string]float64{"speed_kph": 30}
	laterAt := at.Add(time.Second)
	later.ObservedAt = &laterAt
	unknown := event
	unknown.ID = "unknown"
	unknown.Sequence += 2
	unknown.ObservedAt = nil
	recovery := receiverRequest(t, vehicle, event, later, unknown)
	receiverApply(t, s, recovery)
	for i, key := range []string{"native-live", "receiver:unowned-native"} {
		batch, err := MeasuredOBDBatch(map[string]float64{"speed_kph": 40}, nil, nil, nil, at.Add(time.Duration(i+2)*time.Second), key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.IngestSignals(context.Background(), vehicle, batch); err != nil {
			t.Fatal(err)
		}
	}
	smartcar := SignalBatch{Source: "smartcar", BatchID: "smartcar", Observations: []SignalObservation{{Key: "smartcar-fuel", Metric: "fuel_level_pct", Unit: "%", Statistic: "sample", Quality: "measured", Value: 65, ObservedAt: &at}}}
	if _, err = s.IngestSignals(context.Background(), vehicle, smartcar); err != nil {
		t.Fatal(err)
	}
	record := Record{ID: "actual-odo", VehicleID: vehicle, Kind: "odometer", Date: at.Format(time.DateOnly), RecordedAt: &at, Title: "Actual reading", OdometerMiles: 2000, OdometerStatus: "measured"}
	if err = s.SaveEntry(context.Background(), record.ID, vehicle, "record", record, true); err != nil {
		t.Fatal(err)
	}
	return s, recovery, changedImport
}
func applyDiscard(t *testing.T, s *Store) LegacySignalDiscardReport {
	t.Helper()
	ctx := context.Background()
	preview, err := s.DiscardLegacySignals(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.DiscardLegacySignals(ctx, preview.PreviewToken)
	if err != nil || !report.Applied {
		t.Fatalf("apply discard: %+v %v", report, err)
	}
	return report
}

func TestDiscardLegacySignalsPreservesNativeAndDomainData(t *testing.T) {
	s, recovery, _ := discardFixture(t)
	ctx := context.Background()
	before := receiverExport(t, s)
	latest, err := s.LatestSignals(ctx, recovery.VehicleID)
	if err != nil || latest.HistoryRevision != "" {
		t.Fatal("unexpected initial history revision", err)
	}
	preview, err := s.DiscardLegacySignals(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if preview.LubeLoggerSamplesDeleted != 1 || preview.LubeLoggerContextsDeleted != 1 || preview.ReceiverSamplesDeleted != 1 || preview.ReceiverContextsDeleted != 3 || preview.ReceiverArchivesDeleted != 3 || preview.ReusedSamplesPreserved != 2 || preview.ReusedContextsPreserved != 3 || preview.PoliciesCreated != 1 {
		t.Fatalf("wrong preview: %+v", preview)
	}
	if !reflect.DeepEqual(before, receiverExport(t, s)) {
		t.Fatal("preview changed database")
	}
	report, err := s.DiscardLegacySignals(ctx, preview.PreviewToken)
	if err != nil || !report.Applied {
		t.Fatal("apply failed", err)
	}
	after := receiverExport(t, s)
	expected := before
	expected.Signals = []StoredSignal{}
	expected.SignalContexts = []StoredSignalContext{}
	ownedPrefix := "receiver:" + digest([]byte("receiver:created")) + ":"
	for _, signal := range before.Signals {
		if signal.Source != "lubelogger" && !strings.HasPrefix(signal.Observation.Key, ownedPrefix) {
			expected.Signals = append(expected.Signals, signal)
		}
	}
	for _, signal := range before.SignalContexts {
		if signal.Source != "lubelogger" && !strings.HasPrefix(signal.Context.Key, ownedPrefix) {
			expected.SignalContexts = append(expected.SignalContexts, signal)
		}
	}
	expected.ReceiverEventArchives = []ReceiverEventArchive{}
	expected.LegacySignalPolicies = after.LegacySignalPolicies
	if len(after.LegacySignalPolicies) != 1 || !reflect.DeepEqual(expected, after) {
		t.Fatal("cleanup changed retained native/domain/archive/batch data")
	}
	latest, err = s.LatestSignals(ctx, recovery.VehicleID)
	if err != nil || len(latest.HistoryRevision) != 64 {
		t.Fatal("missing history revision", err)
	}
	revision := latest.HistoryRevision
	repeat := applyDiscard(t, s)
	if repeat.LubeLoggerSamplesDeleted+repeat.LubeLoggerContextsDeleted+repeat.ReceiverSamplesDeleted+repeat.ReceiverContextsDeleted+repeat.ReceiverArchivesDeleted+repeat.PoliciesCreated != 0 {
		t.Fatalf("repeat had work: %+v", repeat)
	}
	if !reflect.DeepEqual(after, receiverExport(t, s)) {
		t.Fatal("repeat changed durable state")
	}
	latest, err = s.LatestSignals(ctx, recovery.VehicleID)
	if err != nil || latest.HistoryRevision != revision {
		t.Fatal("repeat changed history revision")
	}
}

func TestDiscardLegacySignalsBlocksReplayButAllowsNativeAndOtherVehicles(t *testing.T) {
	s, recovery, changedImport := discardFixture(t)
	ctx := context.Background()
	before := receiverExport(t, s)
	archive := before.ConvertedSignalNotes[0]
	applyDiscard(t, s)
	clean := receiverExport(t, s)
	if _, err := s.IngestSignals(ctx, recovery.VehicleID, archive.Batch); !errors.Is(err, ErrLegacySignalsDiscarded) {
		t.Fatalf("LL replay: %v", err)
	}
	if _, err := s.RecoverReceiver(ctx, recovery); !errors.Is(err, ErrLegacySignalsDiscarded) {
		t.Fatalf("receiver replay: %v", err)
	}
	originalHash, err := SignalNoteHash(archive.Original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConvertSignalNotes(ctx, []SignalNoteConversion{{NoteID: archive.NoteID, ExpectedHash: originalHash, Batch: archive.Batch}}, ""); !errors.Is(err, ErrLegacySignalsDiscarded) {
		t.Fatalf("conversion replay: %v", err)
	}
	if _, err = s.Import(ctx, changedImport, ""); !errors.Is(err, ErrLegacySignalsDiscarded) {
		t.Fatalf("summary refresh: %v", err)
	}
	if !reflect.DeepEqual(clean, receiverExport(t, s)) {
		t.Fatal("blocked replay changed retained state")
	}
	imported := applyFixture(t, s, importFixtureBatch(t))
	if imported.Created != 0 || imported.Updated != 0 {
		t.Fatal("unchanged source recreated converted notes")
	}
	if !reflect.DeepEqual(clean, receiverExport(t, s)) {
		t.Fatal("unchanged import changed cleaned history")
	}
	at := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	native, _ := MeasuredOBDBatch(map[string]float64{"speed_kph": 50}, nil, nil, nil, at, "new-native")
	if _, err = s.IngestSignals(ctx, recovery.VehicleID, native); err != nil {
		t.Fatal("native ingestion blocked", err)
	}
	if err = s.CreateVehicle(ctx, Vehicle{ID: "other", Name: "Other", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestSignals(ctx, "other", archive.Batch); err != nil {
		t.Fatal("unaffected vehicle blocked", err)
	}
	latest, err := s.LatestSignals(ctx, "other")
	if err != nil || latest.HistoryRevision != "" {
		t.Fatal("unaffected vehicle received history revision")
	}
}

func TestDiscardLegacySignalsStaleAndCorruptOwnershipRollback(t *testing.T) {
	for _, kind := range []string{"native-upload", "changed-owned-value", "changed-reused-value", "missing-owned-row", "archive-projection", "archive-raw", "archive-sequence", "batch-marker", "projection-remapped", "delete-failure"} {
		t.Run(kind, func(t *testing.T) {
			s, recovery, _ := discardFixture(t)
			ctx := context.Background()
			preview, err := s.DiscardLegacySignals(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			token := ""
			switch kind {
			case "native-upload":
				at := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
				batch, _ := MeasuredOBDBatch(map[string]float64{"speed_kph": 50}, nil, nil, nil, at, "concurrent")
				_, err = s.IngestSignals(ctx, recovery.VehicleID, batch)
				token = preview.PreviewToken
			case "changed-owned-value":
				_, err = s.db.Exec("UPDATE signals SET data=json_set(data,'$.value',999) WHERE source='pi' AND key LIKE ?", "receiver:"+digest([]byte("receiver:created"))+":%")
			case "changed-reused-value":
				_, err = s.db.Exec("UPDATE signals SET data=json_set(data,'$.value',999) WHERE source='pi' AND key='native-original:speed_kph'")
			case "missing-owned-row":
				_, err = s.db.Exec("DELETE FROM signals WHERE source='pi' AND key LIKE ?", "receiver:"+digest([]byte("receiver:created"))+":%")
			case "archive-projection":
				_, err = s.db.Exec("UPDATE receiver_event_archives SET projection=json_set(projection,'$.observations',json('[]')) WHERE event_id='created'")
			case "archive-raw":
				_, err = s.db.Exec("UPDATE receiver_event_archives SET raw=json_set(raw,'$.id','wrong') WHERE event_id='created'")
			case "archive-sequence":
				_, err = s.db.Exec("UPDATE receiver_event_archives SET sequence='999' WHERE event_id='created'")
			case "batch-marker":
				_, err = s.db.Exec("UPDATE signal_batches SET hash='changed' WHERE batch_id=?", "receiver:"+digest([]byte("receiver:created")))
			case "projection-remapped":
				// Equal values are insufficient ownership proof. A corrupted archive
				// must not redirect all of its owned rows to native lookalikes.
				at := time.Date(2026, 9, 24, 22, 6, 46, 687965828, time.UTC)
				lookalike, e := MeasuredOBDBatch(map[string]float64{"speed_kph": 30}, []string{}, nil, nil, at, "lookalike")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.IngestSignals(ctx, recovery.VehicleID, lookalike); e != nil {
					t.Fatal(e)
				}
				_, err = s.db.Exec("UPDATE receiver_event_archives SET projection=json_set(projection,'$.observations[0].key','lookalike:speed_kph','$.contexts[0].key','lookalike:pending','$.contexts[1].key','lookalike:permanent','$.contexts[2].key','lookalike:stored') WHERE event_id='created'")
			case "delete-failure":
				_, err = s.db.Exec("CREATE TRIGGER reject_discard BEFORE DELETE ON signal_contexts BEGIN SELECT RAISE(ABORT,'synthetic discard failure'); END")
			}
			if err != nil {
				t.Fatal(err)
			}
			before := receiverExport(t, s)
			if _, err = s.DiscardLegacySignals(ctx, token); err == nil {
				t.Fatal("accepted stale/corrupt ownership or partial deletion")
			}
			if !reflect.DeepEqual(before, receiverExport(t, s)) {
				t.Fatal("failure left partial cleanup")
			}
		})
	}
}

func TestDiscardLegacySignalsSharedRecoveredRowsHaveOneOwner(t *testing.T) {
	s, request, event := receiverFixture(t)
	duplicate := event
	duplicate.ID = "other-event"
	duplicate.Sequence++
	request = receiverRequest(t, request.VehicleID, event, duplicate)
	recovery := receiverApply(t, s, request)
	if recovery.SamplesCreated != 2 || recovery.SamplesReused != 2 {
		t.Fatal("fixture did not share recovered rows")
	}
	report := applyDiscard(t, s)
	if report.ReceiverArchivesDeleted != 2 || report.ReceiverSamplesDeleted != 2 || report.ReceiverContextsDeleted != 3 || report.ReusedSamplesPreserved != 0 || report.ReusedContextsPreserved != 0 {
		t.Fatalf("shared recovered ownership: %+v", report)
	}
	exported := receiverExport(t, s)
	if len(exported.Signals) != 0 || len(exported.SignalContexts) != 0 || len(exported.ReceiverEventArchives) != 0 {
		t.Fatal("shared recovered rows survived cleanup")
	}
}

func TestDiscardLegacySignalsBackupReopenPreservesPolicy(t *testing.T) {
	s, recovery, _ := discardFixture(t)
	applyDiscard(t, s)
	before := receiverExport(t, s)
	var sequence int
	var name, filename string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &filename); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := Backup(context.Background(), filename, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var version int
	if err = restored.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 8 {
		t.Fatal("schema downgrade on reopen", version, err)
	}
	if !reflect.DeepEqual(before, receiverExport(t, restored)) {
		t.Fatal("backup lost deletion policy or retained data")
	}
	if _, err = restored.RecoverReceiver(context.Background(), recovery); !errors.Is(err, ErrLegacySignalsDiscarded) {
		t.Fatal("restored backup allowed receiver resurrection", err)
	}
	raw, err := json.Marshal(receiverExport(t, restored))
	if err != nil || !strings.Contains(string(raw), `"legacySignalPolicies"`) {
		t.Fatal("export omitted deletion policy")
	}
}
