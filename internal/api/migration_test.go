package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/lubelogger"
)

const migrationPreviewPath = "/api/v1/migrations/lubelogger/preview"
const migrationApplyPath = "/api/v1/migrations/lubelogger/apply"

func migrationFixtureRequest() lubelogger.Request {
	return lubelogger.Request{
		Options: lubelogger.Options{Source: "synthetic-private-source", Timezone: "UTC", Currency: "USD", DistanceUnit: "mi", FuelUnit: "us-gal"},
		Export: lubelogger.Export{FormatVersion: 1, Collections: map[string][]json.RawMessage{
			"vehicles": {json.RawMessage(`{"_id":1,"Year":2015,"Make":"Synthetic private make","Model":"Truck","LicensePlate":"synthetic-private-plate"}`)},
			"notes":    {json.RawMessage(`{"_id":1,"VehicleId":1,"Description":"synthetic-private-title","NoteText":"synthetic-private-body","Pinned":true,"ExtraFields":[{"Name":"Private field","Value":"synthetic-private-value","IsRequired":false,"FieldType":"Text"}]}`)},
		}},
	}
}

func migrationBody(t *testing.T, input lubelogger.Request) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func migrationReport(t *testing.T, response *httptest.ResponseRecorder, status int) garage.ImportReport {
	t.Helper()
	if response.Code != status {
		t.Fatalf("migration returned %d, expected %d: %s", response.Code, status, response.Body.String())
	}
	var report garage.ImportReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func migrationExport(t *testing.T, store *garage.Store) garage.Export {
	t.Helper()
	export, err := store.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return export
}

func TestMigrationRoutesRequireAuthenticationAndJSON(t *testing.T) {
	s, h := fixture(t)
	for _, path := range []string{migrationPreviewPath, migrationApplyPath} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated migration returned %d", w.Code)
		}
		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "text/plain")
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("non-JSON migration returned %d", w.Code)
		}
	}
	w := request(h, http.MethodPost, migrationApplyPath, migrationBody(t, migrationFixtureRequest()))
	if w.Code != 422 {
		t.Fatalf("apply without preview returned %d", w.Code)
	}
	export := migrationExport(t, s)
	if len(export.Vehicles)+len(export.Records)+len(export.ImportSources) != 0 {
		t.Fatal("rejected requests changed data")
	}
}

func TestMigrationPreviewApplyAndDuplicateRequests(t *testing.T) {
	s, h := fixture(t)
	input := migrationFixtureRequest()
	preview := migrationReport(t, request(h, http.MethodPost, migrationPreviewPath, migrationBody(t, input)), 200)
	if preview.Created != 2 || preview.Applied || preview.PreviewToken == "" {
		t.Fatalf("invalid preview: %+v", preview)
	}
	export := migrationExport(t, s)
	if len(export.Vehicles)+len(export.Records)+len(export.ImportSources) != 0 {
		t.Fatal("preview wrote data")
	}
	input.PreviewToken = preview.PreviewToken
	applied := migrationReport(t, request(h, http.MethodPost, migrationApplyPath, migrationBody(t, input)), 200)
	if !applied.Applied || applied.Created != 2 {
		t.Fatalf("invalid apply: %+v", applied)
	}
	retry := migrationReport(t, request(h, http.MethodPost, migrationApplyPath, migrationBody(t, input)), 409)
	if retry.Applied {
		t.Fatal("replayed token applied twice")
	}
	input.PreviewToken = ""
	preview = migrationReport(t, request(h, http.MethodPost, migrationPreviewPath, migrationBody(t, input)), 200)
	if preview.Skipped != 2 || preview.Created != 0 {
		t.Fatalf("repeat preview did not deduplicate: %+v", preview)
	}
	input.PreviewToken = preview.PreviewToken
	applied = migrationReport(t, request(h, http.MethodPost, migrationApplyPath, migrationBody(t, input)), 200)
	if !applied.Applied || applied.Skipped != 2 || applied.Created != 0 {
		t.Fatalf("repeat apply did not deduplicate: %+v", applied)
	}
	export = migrationExport(t, s)
	if len(export.Vehicles) != 1 || len(export.Records) != 1 || len(export.ImportSources) != 2 {
		t.Fatal("retries duplicated or lost records")
	}
}

func TestMigrationStaleTargetReturnsConflictWithoutChangingData(t *testing.T) {
	s, h := fixture(t)
	input := migrationFixtureRequest()
	preview := migrationReport(t, request(h, http.MethodPost, migrationPreviewPath, migrationBody(t, input)), 200)
	input.PreviewToken = preview.PreviewToken
	migrationReport(t, request(h, http.MethodPost, migrationApplyPath, migrationBody(t, input)), 200)
	input.PreviewToken = ""
	preview = migrationReport(t, request(h, http.MethodPost, migrationPreviewPath, migrationBody(t, input)), 200)
	id := garage.ImportID(input.Options.Source, "notes", "1")
	w := request(h, http.MethodPatch, "/api/v1/records/"+id, `{"notes":"Edited after preview"}`)
	if w.Code != 200 {
		t.Fatalf("user edit: %d %s", w.Code, w.Body.String())
	}
	before := migrationExport(t, s)
	input.PreviewToken = preview.PreviewToken
	conflict := migrationReport(t, request(h, http.MethodPost, migrationApplyPath, migrationBody(t, input)), 409)
	if conflict.Applied {
		t.Fatal("stale preview applied")
	}
	after := migrationExport(t, s)
	before.ExportedAt = after.ExportedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("conflicted request changed targets or provenance")
	}
}

func TestMigrationUnsupportedSourceDoesNotMutate(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	before := migrationExport(t, s)
	input := migrationFixtureRequest()
	input.Export.Collections["unsupportedrecords"] = []json.RawMessage{json.RawMessage(`{"_id":1,"Unknown":"synthetic"}`)}
	for _, path := range []string{migrationPreviewPath, migrationApplyPath} {
		input.PreviewToken = "synthetic-invalid-preview-token"
		w := request(h, http.MethodPost, path, migrationBody(t, input))
		if w.Code != 422 {
			t.Fatalf("unsupported source returned %d", w.Code)
		}
	}
	after := migrationExport(t, s)
	before.ExportedAt = after.ExportedAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid source modified existing data")
	}
	if len(after.Vehicles) != 1 || !bytes.Contains(after.Vehicles[0], []byte(v.ID)) {
		t.Fatal("unrelated vehicle changed")
	}
}

func TestMigrationTelemetryExcludesSourceDocumentsAndIdentifiers(t *testing.T) {
	s, _ := fixture(t)
	var logs bytes.Buffer
	h, err := New(s, testToken, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	input := migrationFixtureRequest()
	preview := migrationReport(t, request(h, http.MethodPost, migrationPreviewPath+"?private=synthetic-private-query", migrationBody(t, input)), 200)
	input.PreviewToken = preview.PreviewToken
	migrationReport(t, request(h, http.MethodPost, migrationApplyPath, migrationBody(t, input)), 200)
	for _, private := range []string{testToken, input.Options.Source, "Synthetic private make", "synthetic-private-plate", "synthetic-private-title", "synthetic-private-body", "synthetic-private-value", "synthetic-private-query", preview.PreviewToken, garage.ImportID(input.Options.Source, "notes", "1")} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("private migration input appeared in telemetry")
		}
	}
	if !strings.Contains(logs.String(), "migration reconciled") || !strings.Contains(logs.String(), migrationPreviewPath) || !strings.Contains(logs.String(), migrationApplyPath) {
		t.Fatal("migration operation telemetry missing")
	}
}
