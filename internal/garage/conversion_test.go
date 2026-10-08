package garage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func conversionFixture(t *testing.T, s *Store, sourceID string) SignalNoteConversion {
	t.Helper()
	id := ImportID(importFixtureSource, "notes", sourceID)
	raw, err := s.Entry(context.Background(), id, "record")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := SignalNoteHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return SignalNoteConversion{NoteID: id, ExpectedHash: hash, Batch: SignalBatch{Source: "lubelogger", BatchID: id,
		Observations: []SignalObservation{{Key: id + "-pressure", Metric: "manifold_kpa", Unit: "kPa", Statistic: "sample", Quality: "measured", Value: 42, ObservedAt: &observed}}}}
}

func assertConversionUnchanged(t *testing.T, s *Store, before Export) {
	t.Helper()
	after, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before.ExportedAt = after.ExportedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed or preview conversion changed durable data")
	}
}

func TestSignalConversionAtomicArchiveAndImportLedger(t *testing.T) {
	s := importStore(t)
	batch := importFixtureBatch(t)
	applyFixture(t, s, batch)
	conversion := conversionFixture(t, s, "1")
	before, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.ConvertSignalNotes(context.Background(), []SignalNoteConversion{conversion}, "")
	if err != nil || preview.Converted != 1 || preview.Applied {
		t.Fatalf("preview failed: %+v %v", preview, err)
	}
	assertConversionUnchanged(t, s, before)
	again, err := s.ConvertSignalNotes(context.Background(), []SignalNoteConversion{conversion}, "")
	if err != nil || again.PreviewToken != preview.PreviewToken {
		t.Fatal("preview was not deterministic")
	}
	applied, err := s.ConvertSignalNotes(context.Background(), []SignalNoteConversion{conversion}, preview.PreviewToken)
	if err != nil || !applied.Applied || applied.Converted != 1 {
		t.Fatalf("apply failed: %+v %v", applied, err)
	}
	if _, err = s.Entry(context.Background(), conversion.NoteID, "record"); !errors.Is(err, ErrNotFound) {
		t.Fatal("converted visible note still exists")
	}
	after, err := s.Export(context.Background())
	if err != nil || len(after.ConvertedSignalNotes) != 1 {
		t.Fatalf("source archive missing: %v", err)
	}
	var archived Record
	if json.Unmarshal(after.ConvertedSignalNotes[0].Original, &archived) != nil || archived.Notes != "First source text" {
		t.Fatal("original source evidence changed")
	}
	repeated, err := s.ConvertSignalNotes(context.Background(), []SignalNoteConversion{conversion}, "")
	if err != nil || repeated.Skipped != 1 || repeated.Converted != 0 {
		t.Fatal("repeated conversion did not skip archive")
	}
	imported := applyFixture(t, s, batch)
	if imported.Skipped != len(batch.Items) {
		t.Fatal("unchanged source resurrected converted note")
	}
	replaceFixtureNote(t, &batch, "1", "1", "Updated source summary")
	conflict, err := s.Import(context.Background(), batch, "")
	if err != nil || conflict.Conflicts != 1 || conflict.ConflictDetails[0].Reason != "converted-summary-source-changed" {
		t.Fatalf("changed converted source was not protected: %+v %v", conflict, err)
	}
}

func TestSignalConversionFailureRollsBackSignalsArchiveAndDeletion(t *testing.T) {
	for _, scenario := range []string{"changed note", "archive failure", "deletion failure", "invalid second batch", "stale token"} {
		t.Run(scenario, func(t *testing.T) {
			s := importStore(t)
			applyFixture(t, s, importFixtureBatch(t))
			conversions := []SignalNoteConversion{conversionFixture(t, s, "1"), conversionFixture(t, s, "2")}
			preview, err := s.ConvertSignalNotes(context.Background(), conversions, "")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "changed note":
				_, err = s.UpdateEntry(context.Background(), conversions[1].NoteID, "record", func(fields map[string]json.RawMessage) error {
					fields["notes"] = json.RawMessage(`"User changed this"`)
					return nil
				})
			case "archive failure":
				_, err = s.db.Exec(`CREATE TRIGGER reject_conversion BEFORE INSERT ON converted_signal_notes BEGIN SELECT RAISE(ABORT,'synthetic archive failure'); END`)
			case "deletion failure":
				_, err = s.db.Exec(`CREATE TRIGGER reject_deletion BEFORE DELETE ON entries BEGIN SELECT RAISE(ABORT,'synthetic deletion failure'); END`)
			case "invalid second batch":
				conversions[1].Batch.Observations[0].Metric = "unknown_signal"
			case "stale token":
				preview.PreviewToken = "wrong"
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Export(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ConvertSignalNotes(context.Background(), conversions, preview.PreviewToken); err == nil {
				t.Fatal("invalid conversion committed")
			}
			assertConversionUnchanged(t, s, before)
		})
	}
}
