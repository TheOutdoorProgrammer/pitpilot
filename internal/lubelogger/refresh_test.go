package lubelogger

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/summarymetrics"
)

func generatedSummaryRequest(t *testing.T) Request {
	t.Helper()
	r := syntheticRequest(t)
	r.Export.Collections["notes"] = []json.RawMessage{syntheticJSON(t, map[string]any{
		"_id": 1, "VehicleId": 1, "Description": "Daily Status \u2014 2026-01-02", "Tags": []string{"smartcar", "daily-status", "daily-2026-01-02"},
		"NoteText": "## Vehicle Status \u2014 2026-01-02\n\n**Fuel Level:** 50% (25.0 L remaining)",
	})}
	return r
}

func TestRefreshUsesStrictParserAndPreservesInterpretation(t *testing.T) {
	r := generatedSummaryRequest(t)
	before, _, e := Convert(r)
	if e != nil {
		t.Fatal(e)
	}
	if len(before.ConvertedSummaries) != 0 {
		t.Fatal("refresh enabled by default")
	}
	r.RefreshConvertedSummaries = true
	after, _, e := Convert(r)
	if e != nil {
		t.Fatal(e)
	}
	if len(after.ConvertedSummaries) != 1 || !reflect.DeepEqual(before.Settings, after.Settings) || !reflect.DeepEqual(before.Items, after.Items) {
		t.Fatal("refresh altered interpretation or source")
	}
	for name, change := range map[string]func(*Request){
		"pinned":       func(r *Request) { changeSynthetic(t, r, "notes", "Pinned", true) },
		"custom prose": func(r *Request) { changeSynthetic(t, r, "notes", "NoteText", "A user wrote this") },
		"custom title": func(r *Request) { changeSynthetic(t, r, "notes", "Description", "Personal note") },
		"custom fields": func(r *Request) {
			changeSynthetic(t, r, "notes", "ExtraFields", []map[string]any{{"Name": "Keep", "Value": "This", "IsRequired": false, "FieldType": "Text"}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := generatedSummaryRequest(t)
			r.RefreshConvertedSummaries = true
			change(&r)
			b, _, e := Convert(r)
			if e != nil || len(b.ConvertedSummaries) != 0 {
				t.Fatalf("unsupported note eligible: %v", e)
			}
		})
	}
}

func TestRefreshRealParserThroughMigrationAndConversion(t *testing.T) {
	s, e := garage.Open(filepath.Join(t.TempDir(), "garage.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	apply := func(r Request) garage.ImportReport {
		t.Helper()
		b, _, e := Convert(r)
		if e != nil {
			t.Fatal(e)
		}
		p, e := s.Import(ctx, b, "")
		if e != nil || p.Conflicts != 0 {
			t.Fatalf("preview: %+v %v", p, e)
		}
		a, e := s.Import(ctx, b, p.PreviewToken)
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	r := generatedSummaryRequest(t)
	apply(r)
	p, e := summarymetrics.Convert(ctx, s, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = summarymetrics.Convert(ctx, s, p.PreviewToken); e != nil {
		t.Fatal(e)
	}
	r.RefreshConvertedSummaries = true
	changeSynthetic(t, &r, "notes", "NoteText", "## Vehicle Status \u2014 2026-01-02\n\n**Fuel Level:** 60% (30.0 L remaining)\n**Oil Life:** 70%")
	a := apply(r)
	if a.Updated != 1 {
		t.Fatalf("updated: %+v", a)
	}
	out, e := s.Export(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.ConvertedSignalNotes) != 1 || len(out.ConvertedSignalNotes[0].PreviousVersions) != 1 || len(out.Signals) != 3 {
		t.Fatal("incomplete refresh export")
	}
	if len(out.ConvertedSignalNotes[0].PreviousVersions[0].StoredBatch.Observations) != 2 {
		t.Fatal("original readings missing")
	}
	if repeated := apply(r); repeated.Updated != 0 {
		t.Fatal("repeat changed data")
	}
}
