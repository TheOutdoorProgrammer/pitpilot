package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestSignalPreviewsValidationAndAuthorization(t *testing.T) {
	_, handler := fixture(t)
	v := vehicle(t, handler)
	base := "/api/v1/vehicles/" + v.ID + "/signals/previews"
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"?metrics=rpm,speed_kph&maxPoints=10", 200},
		{"", 422}, {"?metrics=rpm,rpm", 422}, {"?metrics=unknown", 422},
		{"?metrics=rpm&maxPoints=11", 422}, {"?metrics=rpm&maxPoints=0", 422},
		{"?metrics=rpm&maxPoints=abc", 400}, {"?metrics=rpm&metrics=speed_kph", 400},
		{"?metrics=rpm&from=private", 400},
	} {
		w := request(handler, "GET", base+tc.query, "")
		if w.Code != tc.status {
			t.Fatalf("%s status%d: %s", tc.query, w.Code, w.Body)
		}
		if w.Code == 200 {
			var out garage.SignalPreviews
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Previews) != 2 || out.Previews[0].Scope != "empty" {
				t.Fatal("invalid empty preview response")
			}
		}
	}
	metrics := garage.SignalDefinitions()
	names := make([]string, 33)
	for i := range names {
		names[i] = metrics[i].Metric
	}
	if w := request(handler, "GET", base+"?metrics="+strings.Join(names, ","), ""); w.Code != 422 {
		t.Fatal("unbounded metric list accepted")
	}
	for _, token := range []string{"", "not-the-api-token"} {
		r := httptest.NewRequest("GET", base+"?metrics=rpm", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("private previews exposed without authorization")
		}
	}
	if w := request(handler, "GET", "/api/v1/vehicles/missing/signals/previews?metrics=rpm", ""); w.Code != 404 {
		t.Fatal("missing vehicle not reported")
	}
	for _, binning := range []string{"time", "adaptive", "wrong"} {
		w := request(handler, "GET", "/api/v1/vehicles/"+v.ID+"/signals/history?metric=rpm&binning="+binning, "")
		want := 200
		if binning == "wrong" {
			want = 422
		}
		if w.Code != want {
			t.Fatal("history binning validation", binning, w.Code)
		}
	}
	if w := request(handler, "POST", "/api/v1/client-events", `{"operation":"signals.previews","durationMs":1,"statusCode":200}`); w.Code != 204 {
		t.Fatal("preview telemetry rejected", w.Code)
	}
}
