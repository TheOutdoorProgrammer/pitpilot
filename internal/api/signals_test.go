package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestSignalAPIAndReadOnlyMetricsAuthentication(t *testing.T) {
	store, disabled := fixture(t)
	v := vehicle(t, disabled)
	base := "/api/v1/vehicles/" + v.ID + "/signals"
	at := time.Now().UTC()
	batch := garage.SignalBatch{Source: "pi", BatchID: "synthetic-private-batch", Observations: []garage.SignalObservation{{Key: "synthetic-private-key", Metric: "manifold_kpa", Unit: "kPa", Statistic: "sample", Quality: "measured", Value: 42, ObservedAt: &at}}}
	raw, _ := json.Marshal(batch)
	if w := request(disabled, "POST", base, string(raw)); w.Code != 200 {
		t.Fatalf("ingest %d %s", w.Code, w.Body)
	}
	if w := request(disabled, "POST", base, string(raw)); w.Code != 200 || !strings.Contains(w.Body.String(), `"skipped":1`) {
		t.Fatalf("duplicate %d %s", w.Code, w.Body)
	}
	changed := batch
	changed.Observations[0].Value = 80
	changedRaw, _ := json.Marshal(changed)
	if w := request(disabled, "POST", base, string(changedRaw)); w.Code != 409 {
		t.Fatalf("conflict %d %s", w.Code, w.Body)
	}
	if w := request(disabled, "GET", base+"/latest", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"value":42`) {
		t.Fatalf("latest %d %s", w.Code, w.Body)
	}
	if w := request(disabled, "GET", base+"/history?metric=manifold_kpa&maxPoints=0", ""); w.Code != 422 {
		t.Fatal("invalid history bounds accepted")
	}
	if w := request(disabled, "GET", base+"/history?metric=manifold_kpa&unknown=private", ""); w.Code != 400 {
		t.Fatal("unknown history query accepted")
	}
	if w := request(disabled, "GET", "/metrics", ""); w.Code != 404 {
		t.Fatal("metrics enabled without token")
	}
	scrapeToken := "separate-read-only-scrape-token-32-characters"
	periodStart, periodEnd := at.Add(-time.Hour), at
	countBatch := garage.SignalBatch{Source: "pi", BatchID: "count-statistic", Observations: []garage.SignalObservation{{Key: "pressure-reading-count", Metric: "manifold_kpa", Unit: "count", Statistic: "count", Quality: "derived", Value: 7, PeriodStart: &periodStart, PeriodEnd: &periodEnd}}}
	if _, err := store.IngestSignals(context.Background(), v.ID, countBatch); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler, err := NewWithOptions(store, testToken, slog.New(slog.NewJSONHandler(&logs, nil)), Options{MetricsToken: scrapeToken})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, token string
		want        int
	}{{"/metrics", "", 401}, {"/metrics", testToken, 401}, {"/metrics", scrapeToken, 200}, {"/api/v1/vehicles", scrapeToken, 401}, {"/api/v1/vehicles", testToken, 200}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s got%d want%d", tc.path, w.Code, tc.want)
		}
		if tc.path == "/metrics" && w.Code == 200 {
			body := w.Body.String()
			if !strings.Contains(body, "pitpilot_vehicle_manifold_kpa_observation_count{") {
				t.Fatal("count statistic did not get its own metric family")
			}
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "pitpilot_vehicle_manifold_kpa{") && strings.Contains(line, `unit="count"`) {
					t.Fatal("pressure metric family contains count units")
				}
			}
			if !strings.Contains(w.Header().Get("Content-Type"), "application/openmetrics-text") || !strings.HasSuffix(body, "# EOF\n") || !strings.Contains(body, "pitpilot_vehicle_manifold_kpa{") || !strings.Contains(body, "_observed_timestamp_seconds") {
				t.Fatal("invalid OpenMetrics exposition", body)
			}
			for _, secret := range []string{v.Name, "synthetic-private-key", "synthetic-private-batch", testToken, scrapeToken} {
				if strings.Contains(body, secret) {
					t.Fatal("private context exposed in metrics")
				}
			}
		}
	}
	for _, secret := range []string{v.ID, v.Name, "manifold_kpa", "synthetic-private", testToken, scrapeToken} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("business data exposed in operational logs")
		}
	}
	for _, token := range []string{testToken, "short", scrapeToken + "\n"} {
		if _, err := NewWithOptions(store, testToken, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{MetricsToken: token}); err == nil {
			t.Fatal("unsafe metrics token accepted")
		}
	}
	if err := store.DeleteVehicle(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSignalsRejectUnknownNumericValueAndAllowNativeEvents(t *testing.T) {
	_, handler := fixture(t)
	v := vehicle(t, handler)
	base := "/api/v1/vehicles/" + v.ID + "/signals"
	for _, value := range []string{"", `,"value":null`} {
		body := `{"source":"pi","batchId":"bad","observations":[{"key":"sample","metric":"rpm","unit":"rpm","statistic":"sample","quality":"measured","observedAt":"2026-06-01T12:00:00Z"` + value + `}]}`
		if w := request(handler, "POST", base, body); w.Code != 400 {
			t.Fatalf("missing/null value accepted %d", w.Code)
		}
	}
	for _, op := range []string{"signals.latest", "signals.history", "signals.catalog"} {
		if w := request(handler, "POST", "/api/v1/client-events", `{"operation":"`+op+`","durationMs":1,"statusCode":200}`); w.Code != 204 {
			t.Fatal("native signal operation rejected", op, w.Code)
		}
	}
}

func TestSignalContextRequiresExplicitMeasurements(t *testing.T) {
	_, handler := fixture(t)
	v := vehicle(t, handler)
	path := "/api/v1/vehicles/" + v.ID + "/signals"
	for _, detail := range []string{`"kind":"location","location":{"type":"LAST_PARKED"}`, `"kind":"location","Location":{"type":"LAST_PARKED"}`, `"kind":"location","location":{"latitude":0,"longitude":0,"type":"LAST_PARKED"},"Location":{"type":"LAST_PARKED"}`, `"kind":"location","location":{"Latitude":0,"longitude":0,"type":"LAST_PARKED"}`, `"kind":"location","location":{"latitude":null,"longitude":0,"type":"LAST_PARKED"}`, `"kind":"coverage","coverage":{}`, `"kind":"coverage","Coverage":{}`, `"kind":"diagnostic","diagnostic":{"class":"stored","unknown":false}`, `"kind":"diagnostic","diagnostic":{"class":"stored","unknown":false,"successfulReads":1}`, `"kind":"diagnostic","diagnostic":{"class":"stored","unknown":false,"successfulReads":1,"codes":null}`, `"kind":"recording_segment","segment":{"startedAt":"2026-06-01T00:00:00Z","endedAt":"2026-06-01T01:00:00Z","state":"running"}`} {
		body := `{"source":"smartcar","batchId":"missing-context-value","contexts":[{"key":"context","calendarDate":"2026-06-01","timezone":"unknown",` + detail + `}]}`
		if w := request(handler, "POST", path, body); w.Code != 400 {
			t.Fatalf("unknown context measurements fabricated: %d %s", w.Code, w.Body)
		}
	}
	body := `{"source":"smartcar","batchId":"explicit-zero","contexts":[{"key":"zero-location","calendarDate":"2026-06-01","timezone":"unknown","kind":"location","location":{"latitude":0,"longitude":0,"type":"LAST_PARKED"}}]}`
	if w := request(handler, "POST", path, body); w.Code != 200 {
		t.Fatalf("explicit zero rejected: %d %s", w.Code, w.Body)
	}
	for _, diagnostic := range []string{`{"class":"stored","unknown":false,"successfulReads":1,"codes":[]}`, `{"class":"stored","unknown":true,"successfulReads":0,"codes":null}`} {
		body = `{"source":"pi","batchId":"` + garage.NewID() + `","contexts":[{"key":"` + garage.NewID() + `","kind":"diagnostic","observedAt":"2026-06-01T12:00:00Z","diagnostic":` + diagnostic + `}]}`
		if w := request(handler, "POST", path, body); w.Code != 200 {
			t.Fatalf("explicit known empty or unknown diagnostic rejected: %d %s", w.Code, w.Body)
		}
	}
}
