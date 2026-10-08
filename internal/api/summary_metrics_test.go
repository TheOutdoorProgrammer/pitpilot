package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/summarymetrics"
)

func TestSummaryConversionAuthenticationPreservationAndReplay(t *testing.T) {
	store, handler := fixture(t)
	ctx := context.Background()
	vehicle := garage.Vehicle{ID: "conversion-fixture", Name: "Fixture"}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatal(err)
	}
	note := garage.Record{ID: "summary-fixture", VehicleID: vehicle.ID, Kind: "note", Title: "Daily Status \u2014 2026-01-02", Notes: "## Vehicle Status \u2014 2026-01-02\n\n**Fuel Level:** 50%", Tags: []string{"smartcar", "daily-status", "daily-2026-01-02"}, Source: &garage.Source{System: "lubelogger", Collection: "notes", ID: "1"}}
	if err := store.SaveEntry(ctx, note.ID, vehicle.ID, "record", note, true); err != nil {
		t.Fatal(err)
	}
	personal := garage.Record{ID: "personal-note", VehicleID: vehicle.ID, Kind: "note", Title: "Keep this", Notes: "Owner's text"}
	if err := store.SaveEntry(ctx, personal.ID, vehicle.ID, "record", personal, true); err != nil {
		t.Fatal(err)
	}
	previewPath := "/api/v1/migrations/summary-metrics/preview"
	applyPath := "/api/v1/migrations/summary-metrics/apply"
	for _, path := range []string{previewPath, applyPath} {
		response := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, r)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected conversion: %d", response.Code)
		}
	}
	if response := request(handler, http.MethodPost, applyPath, `{}`); response.Code != 422 {
		t.Fatal("conversion applied without preview")
	}
	preview := request(handler, http.MethodPost, previewPath, `{}`)
	var report summarymetrics.Report
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &report) != nil || report.Converted != 1 || report.Preserved != 1 || report.Applied {
		t.Fatalf("bad preview: %d %s", preview.Code, preview.Body)
	}
	if _, err := store.Entry(ctx, note.ID, "record"); err != nil {
		t.Fatal("preview deleted note")
	}
	body, _ := json.Marshal(map[string]string{"previewToken": report.PreviewToken})
	applied := request(handler, http.MethodPost, applyPath, string(body))
	if applied.Code != 200 || json.Unmarshal(applied.Body.Bytes(), &report) != nil || !report.Applied {
		t.Fatalf("apply failed: %d %s", applied.Code, applied.Body)
	}
	if _, err := store.Entry(ctx, personal.ID, "record"); err != nil {
		t.Fatal("conversion removed personal note")
	}
	if replay := request(handler, http.MethodPost, applyPath, string(body)); replay.Code != 409 {
		t.Fatal("stale conversion token accepted")
	}
	latest := request(handler, http.MethodGet, "/api/v1/vehicles/"+vehicle.ID+"/signals/latest", "")
	if latest.Code != 200 || !strings.Contains(latest.Body.String(), "fuel_level_pct") || !strings.Contains(latest.Body.String(), "2026-01-02") {
		t.Fatal("converted measurement is not available to dashboard")
	}
}
