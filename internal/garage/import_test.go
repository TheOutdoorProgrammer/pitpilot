package garage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const importFixtureSource = "synthetic-lubelogger"

func importJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func importVehicleItem(t *testing.T, sourceID, name string) ImportItem {
	t.Helper()
	id := ImportID(importFixtureSource, "vehicles", sourceID)
	return ImportItem{Collection: "vehicles", SourceID: sourceID, Kind: "vehicle", ID: id,
		Raw:  importJSON(t, map[string]any{"_id": json.Number(sourceID), "Make": name, "UnknownVehicleField": "retained"}),
		Data: importJSON(t, Vehicle{ID: id, Name: name, Source: &Source{System: "lubelogger", Instance: importFixtureSource, Collection: "vehicles", ID: sourceID}})}
}

func importNoteItem(t *testing.T, sourceID, vehicleSourceID, text string) ImportItem {
	t.Helper()
	id := ImportID(importFixtureSource, "notes", sourceID)
	vehicleID := ImportID(importFixtureSource, "vehicles", vehicleSourceID)
	return ImportItem{Collection: "notes", SourceID: sourceID, Kind: "record", ID: id, VehicleID: vehicleID,
		Raw:  importJSON(t, map[string]any{"_id": json.Number(sourceID), "VehicleId": json.Number(vehicleSourceID), "NoteText": text, "UnknownSourceField": map[string]any{"decimal": map[string]string{"$numberDecimal": "123.4567890123456789"}}}),
		Data: importJSON(t, Record{ID: id, VehicleID: vehicleID, Kind: "note", Title: "Synthetic note " + sourceID, Notes: text, Source: &Source{System: "lubelogger", Instance: importFixtureSource, Collection: "notes", ID: sourceID}})}
}

func importFixtureBatch(t *testing.T) ImportBatch {
	t.Helper()
	archiveID := ImportID(importFixtureSource, "extrafields", "6")
	archive := json.RawMessage(`{"_id":6,"ExtraFields":[{"Name":"Code","FieldType":"Text","IsRequired":false}]}`)
	return ImportBatch{Source: importFixtureSource, Settings: json.RawMessage(`{"timezone":"UTC","currency":"USD","distanceUnit":"mi","fuelUnit":"us-gal","formatVersion":1}`), Items: []ImportItem{
		importNoteItem(t, "1", "1", "First source text"),
		importVehicleItem(t, "1", "Fixture vehicle"),
		importNoteItem(t, "2", "1", "Second source text"),
		{Collection: "extrafields", SourceID: "6", Kind: "archive", ID: archiveID, Raw: archive, Data: archive},
	}}
}

func importStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "garage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func applyFixture(t *testing.T, s *Store, batch ImportBatch) ImportReport {
	t.Helper()
	preview, err := s.Import(context.Background(), batch, "")
	if err != nil || preview.Conflicts != 0 || preview.PreviewToken == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	applied, err := s.Import(context.Background(), batch, preview.PreviewToken)
	if err != nil || !applied.Applied {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	return applied
}

func assertEmptyImportStore(t *testing.T, s *Store) {
	t.Helper()
	export, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Vehicles)+len(export.Records)+len(export.Reminders)+len(export.Trips)+len(export.ImportSources)+len(export.ImportSettings) != 0 {
		t.Fatal("failed or preview-only import changed the database")
	}
}

func replaceFixtureNote(t *testing.T, batch *ImportBatch, id, vehicleID, text string) {
	t.Helper()
	for i := range batch.Items {
		if batch.Items[i].Collection == "notes" && batch.Items[i].SourceID == id {
			batch.Items[i] = importNoteItem(t, id, vehicleID, text)
			return
		}
	}
	t.Fatal("missing fixture note")
}

func TestImportPreviewApplyAndRepeat(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	preview, err := s.Import(context.Background(), batch, "")
	if err != nil || preview.Created != 4 || preview.Applied || preview.PreviewToken == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	assertEmptyImportStore(t, s)
	again, err := s.Import(context.Background(), batch, "")
	if err != nil || again.PreviewToken != preview.PreviewToken {
		t.Fatal("read-only preview changed state")
	}
	applied := applyFixture(t, s, batch)
	if applied.Created != 4 {
		t.Fatalf("created: %+v", applied)
	}
	repeated := applyFixture(t, s, batch)
	if repeated.Created != 0 || repeated.Updated != 0 || repeated.Skipped != 4 {
		t.Fatalf("duplicate import: %+v", repeated)
	}
	export, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Vehicles) != 1 || len(export.Records) != 2 || len(export.ImportSources) != 4 {
		t.Fatal("reimport duplicated or dropped data")
	}
}

func TestImportUpdatesChangedSourceAndRetainsRemovedSource(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	replaceFixtureNote(t, &batch, "1", "1", "Changed upstream text")
	report := applyFixture(t, s, batch)
	if report.Updated != 1 || report.Skipped != 3 {
		t.Fatalf("changed source: %+v", report)
	}
	raw, err := s.Entry(context.Background(), ImportID(importFixtureSource, "notes", "1"), "record")
	if err != nil {
		t.Fatal(err)
	}
	var note Record
	if err := json.Unmarshal(raw, &note); err != nil || note.Notes != "Changed upstream text" {
		t.Fatalf("source update missing: %s %v", raw, err)
	}
	removed := ImportBatch{Source: batch.Source, Settings: batch.Settings, Items: []ImportItem{importVehicleItem(t, "1", "Fixture vehicle")}}
	report = applyFixture(t, s, removed)
	if report.Retained != 3 {
		t.Fatalf("removed sources not retained: %+v", report)
	}
	entries, err := s.Entries(context.Background(), note.VehicleID, "record")
	if err != nil || len(entries) != 2 {
		t.Fatal("upstream omission deleted target records")
	}
}

func TestImportPreservesUserEditAndRollsBackAllConflictingChanges(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	id := ImportID(importFixtureSource, "notes", "1")
	_, err := s.UpdateEntry(context.Background(), id, "record", func(fields map[string]json.RawMessage) error {
		fields["notes"] = json.RawMessage(`"User edit"`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	report := applyFixture(t, s, batch)
	if report.Skipped != 4 {
		t.Fatalf("unchanged source should preserve edits: %+v", report)
	}
	replaceFixtureNote(t, &batch, "1", "1", "Upstream competing edit")
	replaceFixtureNote(t, &batch, "2", "1", "Otherwise valid update")
	batch.Items = append(batch.Items, importNoteItem(t, "3", "1", "Otherwise valid new note"))
	preview, err := s.Import(context.Background(), batch, "")
	if err != nil || preview.Conflicts != 1 || preview.Updated != 1 || preview.Created != 1 {
		t.Fatalf("conflict preview: %+v %v", preview, err)
	}
	if len(preview.ConflictDetails) != 1 || preview.ConflictDetails[0].TargetID != id || preview.ConflictDetails[0].SourceID != "1" || preview.ConflictDetails[0].Reason != "source-and-target-changed" {
		t.Fatal("conflict does not identify the record requiring reconciliation")
	}
	before, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.ImportSettings) != 1 {
		t.Fatal("source interpretation settings missing from export")
	}
	if _, err = s.Import(context.Background(), batch, preview.PreviewToken); !errors.Is(err, ErrImportConflict) {
		t.Fatalf("conflict applied: %v", err)
	}
	after, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before.ExportedAt = after.ExportedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("conflicted batch partially changed target or provenance")
	}
}

func TestImportAcceptsIndependentlyReconciledEdits(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	id := ImportID(importFixtureSource, "notes", "1")
	_, err := s.UpdateEntry(context.Background(), id, "record", func(fields map[string]json.RawMessage) error {
		fields["notes"] = json.RawMessage(`"Agreed text"`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	replaceFixtureNote(t, &batch, "1", "1", "Agreed text")
	report := applyFixture(t, s, batch)
	if report.Updated != 1 || report.Conflicts != 0 {
		t.Fatalf("matching edits did not reconcile: %+v", report)
	}
	report = applyFixture(t, s, batch)
	if report.Skipped != 4 {
		t.Fatal("reconciled source identity was not retained")
	}
}

func TestImportDeletionIsNotResurrected(t *testing.T) {
	for _, deleteVehicle := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "vehicle cascade"}[deleteVehicle], func(t *testing.T) {
			s := importStore(t)
			batch := importFixtureBatch(t)
			applyFixture(t, s, batch)
			var err error
			if deleteVehicle {
				err = s.DeleteVehicle(context.Background(), ImportID(importFixtureSource, "vehicles", "1"))
			} else {
				err = s.DeleteEntry(context.Background(), ImportID(importFixtureSource, "notes", "1"), "record")
			}
			if err != nil {
				t.Fatal(err)
			}
			preview, err := s.Import(context.Background(), batch, "")
			if err != nil || preview.Conflicts == 0 {
				t.Fatalf("deleted target not conflicted: %+v %v", preview, err)
			}
			if _, err := s.Import(context.Background(), batch, preview.PreviewToken); !errors.Is(err, ErrImportConflict) {
				t.Fatal("deleted target restored")
			}
			if _, err := s.Entry(context.Background(), ImportID(importFixtureSource, "notes", "1"), "record"); !errors.Is(err, ErrNotFound) {
				t.Fatal("deleted record resurrected")
			}
		})
	}
}

func TestImportRejectsPreviewWhenTargetOrSourceChanges(t *testing.T) {
	for _, changeSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "target", true: "source"}[changeSource], func(t *testing.T) {
			s := importStore(t)
			batch := importFixtureBatch(t)
			applyFixture(t, s, batch)
			preview, err := s.Import(context.Background(), batch, "")
			if err != nil {
				t.Fatal(err)
			}
			if changeSource {
				replaceFixtureNote(t, &batch, "1", "1", "Changed after preview")
			} else {
				_, err = s.UpdateEntry(context.Background(), ImportID(importFixtureSource, "notes", "1"), "record", func(fields map[string]json.RawMessage) error {
					fields["title"] = json.RawMessage(`"Changed after preview"`)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Import(context.Background(), batch, preview.PreviewToken); !errors.Is(err, ErrImportConflict) {
				t.Fatalf("stale preview accepted: %v", err)
			}
		})
	}
}

func TestImportReassignsVehicleAndCascadeRelationshipTogether(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	batch.Items = append(batch.Items, importVehicleItem(t, "2", "Second vehicle"))
	applyFixture(t, s, batch)
	replaceFixtureNote(t, &batch, "1", "2", "Moved note")
	applyFixture(t, s, batch)
	oldVehicle := ImportID(importFixtureSource, "vehicles", "1")
	newVehicle := ImportID(importFixtureSource, "vehicles", "2")
	oldEntries, err := s.Entries(context.Background(), oldVehicle, "record")
	if err != nil {
		t.Fatal(err)
	}
	newEntries, err := s.Entries(context.Background(), newVehicle, "record")
	if err != nil {
		t.Fatal(err)
	}
	if len(oldEntries) != 1 || len(newEntries) != 1 {
		t.Fatal("SQL vehicle relationship disagrees with imported record")
	}
	if err := s.DeleteVehicle(context.Background(), oldVehicle); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Entry(context.Background(), ImportID(importFixtureSource, "notes", "1"), "record"); err != nil {
		t.Fatal("old vehicle deletion cascaded to reassigned note")
	}
}

func TestImportInvalidAndInterruptedBatchesLeaveNoData(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(*ImportBatch)
	}{
		{"duplicate identity", func(b *ImportBatch) { b.Items = append(b.Items, b.Items[0]) }},
		{"invalid target", func(b *ImportBatch) { b.Items[0].Data = json.RawMessage(`{"kind":"unsupported"}`) }},
		{"invalid source", func(b *ImportBatch) { b.Items[0].Raw = json.RawMessage(`{broken`) }},
		{"trailing source", func(b *ImportBatch) { b.Items[0].Raw = json.RawMessage(`{"_id":1} {"_id":2}`) }},
		{"duplicate source keys", func(b *ImportBatch) { b.Items[0].Raw = json.RawMessage(`{"_id":1,"_id":2}`) }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			s := importStore(t)
			batch := importFixtureBatch(t)
			mutate.change(&batch)
			if _, err := s.Import(context.Background(), batch, ""); err == nil {
				t.Fatal("invalid batch accepted")
			}
			assertEmptyImportStore(t, s)
		})
	}
	t.Run("cancelled apply", func(t *testing.T) {
		s := importStore(t)
		batch := importFixtureBatch(t)
		preview, err := s.Import(context.Background(), batch, "")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.Import(ctx, batch, preview.PreviewToken); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled import: %v", err)
		}
		assertEmptyImportStore(t, s)
	})
	t.Run("storage failure after vehicle write", func(t *testing.T) {
		s := importStore(t)
		batch := importFixtureBatch(t)
		preview, err := s.Import(context.Background(), batch, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON entries BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Import(context.Background(), batch, preview.PreviewToken); err == nil {
			t.Fatal("failed write succeeded")
		}
		assertEmptyImportStore(t, s)
	})
}

func TestImportBackupRestoresRawSourceAndDeduplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	before, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(backup, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before.ExportedAt = after.ExportedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("backup lost source documents or editable targets")
	}
	repeated := applyFixture(t, s, batch)
	if repeated.Skipped != 4 || repeated.Created != 0 {
		t.Fatal("restored database lost migration identity")
	}
	for _, source := range after.ImportSources {
		if source.Collection == "notes" {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(source.Raw, &raw); err != nil {
				t.Fatal(err)
			}
			if raw["UnknownSourceField"] == nil {
				t.Fatal("unknown source metadata missing from backup")
			}
		}
	}
}

func TestConcurrentImportApplicationsDoNotDuplicate(t *testing.T) {
	s := importStore(t)
	preview, err := s.Import(context.Background(), importFixtureBatch(t), "")
	if err != nil {
		t.Fatal(err)
	}
	first, second := importFixtureBatch(t), importFixtureBatch(t)
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, batch := range []ImportBatch{first, second} {
		go func() { <-start; _, err := s.Import(context.Background(), batch, preview.PreviewToken); results <- err }()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrImportConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("applications: successes=%d conflicts=%d", successes, conflicts)
	}
	export, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Vehicles) != 1 || len(export.Records) != 2 || len(export.ImportSources) != 4 {
		t.Fatal("concurrent applications duplicated or dropped data")
	}
}

func TestImportPinsSettingsAndRejectsInterpretationChanges(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	before, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.ImportSettings) != 1 || before.ImportSettings[0].Source != batch.Source {
		t.Fatal("settings were not exported with source identity")
	}
	var pinned map[string]any
	if err := json.Unmarshal(before.ImportSettings[0].Settings, &pinned); err != nil {
		t.Fatal(err)
	}
	if pinned["timezone"] != "UTC" || pinned["fuelUnit"] != "us-gal" {
		t.Fatal("source interpretation was not pinned")
	}
	preview, err := s.Import(context.Background(), batch, "")
	if err != nil {
		t.Fatal(err)
	}
	batch.Settings = json.RawMessage(`{"formatVersion":1,"fuelUnit":"us-gal","distanceUnit":"mi","currency":"USD","timezone":"UTC"}`)
	reordered, err := s.Import(context.Background(), batch, "")
	if err != nil || reordered.PreviewToken != preview.PreviewToken {
		t.Fatal("equivalent settings changed preview identity")
	}
	batch.Settings = json.RawMessage(`{"formatVersion":1,"fuelUnit":"us-gal","distanceUnit":"mi","currency":"USD","timezone":"America/New_York"}`)
	for _, token := range []string{"", preview.PreviewToken} {
		if _, err := s.Import(context.Background(), batch, token); !errors.Is(err, ErrImportSettingsChanged) {
			t.Fatalf("changed source interpretation was accepted: %v", err)
		}
	}
	after, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before.ExportedAt = after.ExportedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("settings conflict changed historical data or source interpretation")
	}
}

func TestImportSettingsRequireCanonicalJSONObject(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `"UTC"`, `{"timezone":"UTC","timezone":"America/New_York"}`, `{} {}`} {
		s := importStore(t)
		batch := importFixtureBatch(t)
		batch.Settings = json.RawMessage(raw)
		if _, err := s.Import(context.Background(), batch, ""); err == nil {
			t.Fatalf("invalid settings accepted: %s", raw)
		}
		assertEmptyImportStore(t, s)
	}
	s := importStore(t)
	batch := importFixtureBatch(t)
	batch.Settings = nil
	applyFixture(t, s, batch)
	export, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(export.ImportSettings) != 1 || string(export.ImportSettings[0].Settings) != `{}` {
		t.Fatal("missing internal settings did not use stable empty object")
	}
}

func TestImportPreviewBindsSettingsBeforeFirstApply(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	preview, err := s.Import(context.Background(), batch, "")
	if err != nil {
		t.Fatal(err)
	}
	batch.Settings = json.RawMessage(`{"timezone":"America/New_York"}`)
	if _, err := s.Import(context.Background(), batch, preview.PreviewToken); !errors.Is(err, ErrImportConflict) {
		t.Fatal("settings changed between first preview and apply")
	}
	assertEmptyImportStore(t, s)
}

func TestOpenEarlySchemaTwoRehearsalAddsSettingsTable(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "early-rehearsal.db")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE import_settings"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	export, err := s.Export(context.Background())
	if err != nil || len(export.ImportSettings) != 1 {
		t.Fatalf("early rehearsal settings missing: %v", err)
	}
}
