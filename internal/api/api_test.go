package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

const testToken = "test-household-token-with-32-characters"

func fixture(t *testing.T) (*garage.Store, http.Handler) {
	t.Helper()
	s, err := garage.Open(filepath.Join(t.TempDir(), "garage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h, err := New(s, testToken, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func vehicle(t *testing.T, h http.Handler) garage.Vehicle {
	t.Helper()
	w := request(h, "POST", "/api/v1/vehicles", `{"name":"Test truck","year":2002,"odometerMiles":143545}`)
	if w.Code != 201 {
		t.Fatalf("vehicle: %d %s", w.Code, w.Body.String())
	}
	var v garage.Vehicle
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAuthenticationAndHealth(t *testing.T) {
	_, h := fixture(t)
	for _, path := range []string{"/api/v1/vehicles", "/api/v1/export", "/api/v1/vehicles/other/records"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", path, w.Code)
		}
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s=%d", path, w.Code)
		}
	}
}

func TestGarageLifecycleAndExport(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	base := "/api/v1/vehicles/" + v.ID
	for _, suffix := range []string{"records", "reminders", "trips"} {
		w := request(h, "GET", base+"/"+suffix, "")
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatalf("empty %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", base+"/records", `{"kind":"fuel","title":"Fill-up","date":"2026-10-08","costCents":4599,"gallons":12.5,"odometerMiles":143600}`)
	if w.Code != 201 {
		t.Fatalf("record %d %s", w.Code, w.Body.String())
	}
	var record garage.Record
	json.Unmarshal(w.Body.Bytes(), &record)
	w = request(h, "POST", base+"/reminders", `{"title":"Oil change","dueOdometerMiles":148545}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var reminder garage.Reminder
	json.Unmarshal(w.Body.Bytes(), &reminder)
	w = request(h, "PATCH", "/api/v1/reminders/"+reminder.ID, `{"completed":true}`)
	if w.Code != 200 {
		t.Fatalf("complete reminder: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &reminder)
	if !reminder.Completed {
		t.Fatal("reminder not completed")
	}
	w = request(h, "POST", base+"/trips", `{"title":"Recorded drive","startedAt":"2026-10-08T12:00:00Z","endedAt":"2026-10-08T12:01:00Z","distanceMiles":0.5,"points":[{"latitude":40,"longitude":-75,"recordedAt":"2026-10-08T12:00:00Z"},{"latitude":40.001,"longitude":-75,"recordedAt":"2026-10-08T12:01:00Z"}]}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = request(h, "PATCH", base, `{"odometerMiles":143600}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var updated garage.Vehicle
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Name != v.Name || updated.OdometerMiles != 143600 {
		t.Fatalf("partial patch lost data: %+v", updated)
	}
	w = request(h, "GET", "/api/v1/export", "")
	var export garage.Export
	if err := json.Unmarshal(w.Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Vehicles) != 1 || len(export.Records) != 1 || len(export.Reminders) != 1 || len(export.Trips) != 1 {
		t.Fatalf("incomplete export: %+v", export)
	}
	w = request(h, "DELETE", base, "")
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = request(h, "DELETE", "/api/v1/records/"+record.ID, "")
	if w.Code != 404 {
		t.Fatalf("orphaned record after vehicle delete: %d", w.Code)
	}
	w = request(h, "GET", "/api/v1/export", "")
	json.Unmarshal(w.Body.Bytes(), &export)
	if len(export.Vehicles)+len(export.Records)+len(export.Reminders)+len(export.Trips) != 0 {
		t.Fatal("cascade did not remove children")
	}
}

func TestValidationAndMissingParents(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/v1/vehicles", `{"name":""}`, 422},
		{"POST", "/api/v1/vehicles", `{"name":"truck","odometerMiles":-1}`, 422},
		{"POST", "/api/v1/vehicles", `{"name":"truck","unexpected":true}`, 400},
		{"POST", "/api/v1/vehicles", `{"name":"truck"} {"name":"second"}`, 400},
		{"POST", "/api/v1/vehicles/" + v.ID + "/records", `{"title":"Bad cost","kind":"fuel","date":"2026-10-08","costCents":-1}`, 422},
		{"POST", "/api/v1/vehicles/" + v.ID + "/records", `{"title":"Bad date","kind":"fuel","date":"2026-02-30"}`, 422},
		{"POST", "/api/v1/vehicles/" + v.ID + "/reminders", `{"title":"No threshold"}`, 422},
		{"POST", "/api/v1/vehicles/missing/records", `{"title":"Oil","kind":"service","date":"2026-10-08"}`, 404},
		{"GET", "/api/v1/vehicles/missing/trips", "", 404},
		{"PATCH", "/api/v1/reminders/missing", `{"completed":true}`, 404},
		{"POST", "/api/v1/vehicles/" + v.ID + "/trips", `{"title":"Bad GPS","startedAt":"2026-10-08T12:00:00Z","endedAt":"2026-10-08T12:01:00Z","points":[{"latitude":95,"longitude":0,"recordedAt":"2026-10-08T12:00:00Z"}]}`, 422},
	}
	for _, tc := range cases {
		t.Run(tc.body+tc.path, func(t *testing.T) {
			w := request(h, tc.method, tc.path, tc.body)
			if w.Code != tc.status {
				t.Fatalf("got %d expected %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "db")
	s, err := garage.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := New(s, testToken, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	v := vehicle(t, h)
	s.Close()
	s, err = garage.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Vehicle(context.Background(), v.ID)
	if err != nil || got.Name != v.Name {
		t.Fatalf("persisted vehicle: %+v %v", got, err)
	}
}

func TestRequestLogsExcludePersonalData(t *testing.T) {
	s, _ := fixture(t)
	var logs bytes.Buffer
	h, _ := New(s, testToken, slog.New(slog.NewJSONHandler(&logs, nil)))
	w := request(h, "POST", "/api/v1/vehicles?secret=private-query", `{"name":"private-name"}`)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	for _, secret := range []string{testToken, "private-query", "private-name"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("sensitive value in logs: %q", secret)
		}
	}
	if !strings.Contains(logs.String(), "POST /api/v1/vehicles") {
		t.Fatal("route template missing")
	}
}

type gatedReader struct {
	source  io.Reader
	entered chan struct{}
	release chan struct{}
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
		<-r.release
	}
	return r.source.Read(p)
}

func TestConcurrentPatchesPreserveDifferentFields(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	path := "/api/v1/vehicles/" + v.ID
	entered, release := make(chan struct{}), make(chan struct{})
	body := &gatedReader{source: strings.NewReader(`{"name":"Renamed truck"}`), entered: entered, release: release}
	r := httptest.NewRequest("PATCH", path, body)
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); h.ServeHTTP(w, r); result <- w }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("request did not read body")
	}
	w := request(h, "PATCH", path, `{"odometerMiles":143650}`)
	close(release)
	if w.Code != 200 {
		t.Fatalf("mileage patch: %d", w.Code)
	}
	select {
	case w = <-result:
		if w.Code != 200 {
			t.Fatalf("name patch: %d", w.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked patch")
	}
	w = request(h, "GET", path, "")
	var got garage.Vehicle
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed truck" || got.OdometerMiles != 143650 {
		t.Fatalf("lost concurrent update: %+v", got)
	}
}

func TestClientTelemetryHasStrictBoundedSchema(t *testing.T) {
	_, h := fixture(t)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"operation":"vehicles.list","durationMs":15,"statusCode":200}`, 204},
		{`{"operation":"vehicle.create","durationMs":50,"statusCode":0}`, 204},
		{`{"operation":"private-vehicle-id","durationMs":15,"statusCode":200}`, 422},
		{`{"operation":"vehicles.list","durationMs":120001,"statusCode":200}`, 422},
		{`{"operation":"vehicles.list","durationMs":15,"statusCode":999}`, 422},
		{`{"operation":"vehicles.list","durationMs":15,"statusCode":200,"url":"private-url"}`, 400},
	} {
		w := request(h, "POST", "/api/v1/client-events", tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d expected %d", tc.body, w.Code, tc.status)
		}
	}
}
