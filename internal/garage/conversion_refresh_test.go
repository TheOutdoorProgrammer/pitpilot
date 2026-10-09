package garage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func refreshFixture(t *testing.T) (*Store, ImportBatch, SignalBatch) {
	t.Helper()
	s := importStore(t)
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	c := conversionFixture(t, s, "1")
	identity := sha256.Sum256([]byte(c.NoteID))
	c.Batch.BatchID = fmt.Sprintf("summary-%x", identity[:16])
	c.Batch.Observations[0].Key = c.Batch.BatchID + "-0000"
	c.Batch.Contexts = []SignalContext{{Key: c.Batch.BatchID + "-snapshot-0", Kind: "snapshot", CalendarDate: "2026-06-01", Timezone: "unknown", Snapshot: &SignalSnapshot{Date: "2026-06-01", Timezone: "unknown", Precision: "day"}}}
	p, err := s.ConvertSignalNotes(context.Background(), []SignalNoteConversion{c}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConvertSignalNotes(context.Background(), []SignalNoteConversion{c}, p.PreviewToken); err != nil {
		t.Fatal(err)
	}
	replaceFixtureNote(t, &batch, "1", "1", "Updated source text")
	replacement := c.Batch
	replacement.Observations = append([]SignalObservation(nil), c.Batch.Observations...)
	replacement.Observations[0].Value = 50
	batch.ConvertedSummaries = map[string]SignalBatch{c.NoteID: replacement}
	return s, batch, c.Batch
}

func TestRefreshConvertedSummaryArchivesReconcilesAndRepeats(t *testing.T) {
	s, batch, old := refreshFixture(t)
	ctx := context.Background()
	before, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.Import(ctx, batch, "")
	if err != nil || preview.Updated != 1 || preview.Conflicts != 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	assertConversionUnchanged(t, s, before)
	again, err := s.Import(ctx, batch, "")
	if err != nil || again.PreviewToken != preview.PreviewToken {
		t.Fatal("nondeterministic preview", err)
	}
	if _, err = s.Import(ctx, batch, preview.PreviewToken); err != nil {
		t.Fatal(err)
	}
	after, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Signals) != len(before.Signals) || len(after.SignalContexts) != len(before.SignalContexts) || len(after.SignalBatches) != len(before.SignalBatches) {
		t.Fatal("refresh duplicated or lost rows")
	}
	archive := after.ConvertedSignalNotes[0]
	if len(archive.PreviousVersions) != 1 {
		t.Fatal("prior version missing")
	}
	version := archive.PreviousVersions[0]
	normalized, _ := normalizeSignalBatch(old)
	if version.Version != 1 || version.ConversionVersion != 1 || !reflect.DeepEqual(version.StoredBatch, normalized) || !reflect.DeepEqual(version.Archive, before.ConvertedSignalNotes[0]) || len(version.SourceRaw) == 0 || len(version.SourceHash) != 64 || len(version.StoredBatchHash) != 64 {
		t.Fatal("incomplete version archive")
	}
	if archive.Batch.Observations[0].Value != 50 || after.Signals[0].Observation.Value != 50 {
		t.Fatal("new measurement missing")
	}
	if _, err = s.Entry(ctx, archive.NoteID, "record"); !errors.Is(err, ErrNotFound) {
		t.Fatal("refresh resurrected visible note")
	}
	repeated := applyFixture(t, s, batch)
	if repeated.Updated != 0 || repeated.Skipped != 4 {
		t.Fatalf("repeat: %+v", repeated)
	}
	assertConversionUnchanged(t, s, after)
	// A later source reversion is another version, not a duplicate archive key.
	replaceFixtureNote(t, &batch, "1", "1", "First source text")
	batch.ConvertedSummaries[archive.NoteID] = old
	applyFixture(t, s, batch)
	reverted, err := s.Export(ctx)
	if err != nil || len(reverted.ConvertedSignalNotes[0].PreviousVersions) != 2 || reverted.Signals[0].Observation.Value != 42 {
		t.Fatal("source reversion lost history", err)
	}
}

func TestRefreshConvertedSummaryProtectsChangedAndMissingData(t *testing.T) {
	cases := map[string]func(*testing.T, *Store, *ImportBatch, SignalBatch){
		"opt in required": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) { b.ConvertedSummaries = nil },
		"signal changed": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("UPDATE signals SET data=json_set(data,'$.value',99)")
			if e != nil {
				t.Fatal(e)
			}
		},
		"signal missing": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("DELETE FROM signals")
			if e != nil {
				t.Fatal(e)
			}
		},
		"context changed": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("UPDATE signal_contexts SET data=json_set(data,'$.timezone','UTC')")
			if e != nil {
				t.Fatal(e)
			}
		},
		"context missing": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("DELETE FROM signal_contexts")
			if e != nil {
				t.Fatal(e)
			}
		},
		"batch changed": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("UPDATE signal_batches SET hash='changed'")
			if e != nil {
				t.Fatal(e)
			}
		},
		"batch missing": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("DELETE FROM signal_batches")
			if e != nil {
				t.Fatal(e)
			}
		},
		"archive changed": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("UPDATE converted_signal_notes SET batch_hash='changed'")
			if e != nil {
				t.Fatal(e)
			}
		},
		"ledger raw changed": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			_, e := s.db.Exec("UPDATE import_sources SET raw='{}' WHERE collection='notes' AND source_id='1'")
			if e != nil {
				t.Fatal(e)
			}
		},
		"vehicle changed": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			b.Items = append(b.Items, importVehicleItem(t, "2", "Other vehicle"))
			replaceFixtureNote(t, b, "1", "2", "Updated source text")
		},
		"visible note restored": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			item := importNoteItem(t, "1", "1", "Native restored note")
			_, e := s.db.Exec("INSERT INTO entries(id,vehicle_id,kind,data) VALUES(?,?,'record',?)", item.ID, item.VehicleID, string(item.Data))
			if e != nil {
				t.Fatal(e)
			}
		},
		"replacement collision": func(t *testing.T, s *Store, b *ImportBatch, old SignalBatch) {
			id := ImportID(importFixtureSource, "notes", "1")
			next := b.ConvertedSummaries[id]
			extra := next.Observations[0]
			extra.Key = old.BatchID + "-0001"
			next.Observations = append(next.Observations, extra)
			b.ConvertedSummaries[id] = next
			other := SignalBatch{Source: old.Source, BatchID: "unrelated", Observations: []SignalObservation{extra}}
			if _, e := s.IngestSignals(context.Background(), ImportID(importFixtureSource, "vehicles", "1"), other); e != nil {
				t.Fatal(e)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, b, old := refreshFixture(t)
			mutate(t, s, &b, old)
			before, e := s.Export(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			p, e := s.Import(context.Background(), b, "")
			if e != nil || p.Conflicts != 1 {
				t.Fatalf("preview: %+v %v", p, e)
			}
			assertConversionUnchanged(t, s, before)
			if _, e = s.Import(context.Background(), b, p.PreviewToken); !errors.Is(e, ErrImportConflict) {
				t.Fatalf("apply: %v", e)
			}
			assertConversionUnchanged(t, s, before)
		})
	}
}

func TestRefreshConvertedSummaryStalePreviewAndUnrelatedSources(t *testing.T) {
	s, b, _ := refreshFixture(t)
	ctx := context.Background()
	p, e := s.Import(ctx, b, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("UPDATE converted_signal_notes SET data=json_set(data,'$.convertedAt','2026-06-02T00:00:00Z')"); e != nil {
		t.Fatal(e)
	}
	before, e := s.Export(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Import(ctx, b, p.PreviewToken); !errors.Is(e, ErrImportConflict) {
		t.Fatalf("stale preview: %v", e)
	}
	assertConversionUnchanged(t, s, before)
	var replacement SignalBatch
	for _, v := range b.ConvertedSummaries {
		replacement = v
	}
	replacement.Source = "pi"
	replacement.BatchID = "native"
	if _, e = s.IngestSignals(ctx, ImportID(importFixtureSource, "vehicles", "1"), replacement); e != nil {
		t.Fatal(e)
	}
	applyFixture(t, s, b)
	after, e := s.Export(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, signal := range after.Signals {
		if signal.Source == "pi" {
			found = true
			actual, _ := json.Marshal(signal.Observation)
			expected, _ := json.Marshal(replacement.Observations[0])
			if string(actual) != string(expected) {
				t.Fatal("native data changed")
			}
		}
	}
	if !found {
		t.Fatal("native source removed")
	}
}

func TestRefreshConvertedSummaryRollsBackWhenAnotherRecordConflicts(t *testing.T) {
	s, batch, _ := refreshFixture(t)
	ctx := context.Background()
	replaceFixtureNote(t, &batch, "2", "1", "Changed upstream")
	native := importNoteItem(t, "2", "1", "Changed by user")
	if _, err := s.db.Exec("UPDATE entries SET data=? WHERE id=?", string(native.Data), native.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.Import(ctx, batch, "")
	if err != nil || preview.Updated != 1 || preview.Conflicts != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	assertConversionUnchanged(t, s, before)
	if _, err = s.Import(ctx, batch, preview.PreviewToken); !errors.Is(err, ErrImportConflict) {
		t.Fatalf("apply: %v", err)
	}
	assertConversionUnchanged(t, s, before)
}
