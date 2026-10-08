package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestRecordPatchPreservesSourceAndUnknownMetadata(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	ctx := context.Background()
	raw := json.RawMessage(`{"id":"record","vehicleId":"` + v.ID + `","kind":"plan","date":"","title":"Planned repair","notes":"Original","costCents":4500,"odometerMiles":0,"plan":{"status":"planned","priority":"normal","recordKind":"repair","reminderIds":["reminder"],"futurePlanValue":"retained"},"source":{"system":"lubelogger","instance":"fixture","collection":"planrecords","id":"1"},"future":{"preserve":true},"extraFields":[{"name":"Part","value":"Exact source value","fieldType":0,"isRequired":false}]}`)
	if err := s.SaveEntry(ctx, "record", v.ID, "record", raw, true); err != nil {
		t.Fatal(err)
	}
	w := request(h, "PATCH", "/api/v1/records/record", `{"title":"Updated repair","plan":{"status":"testing"}}`)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"source", "future", "extraFields"} {
		if got[key] == nil {
			t.Fatalf("missing %s", key)
		}
	}
	var plan map[string]json.RawMessage
	if err := json.Unmarshal(got["plan"], &plan); err != nil {
		t.Fatal(err)
	}
	if string(plan["status"]) != `"testing"` || string(plan["futurePlanValue"]) != `"retained"` || string(plan["reminderIds"]) != `["reminder"]` {
		t.Fatalf("plan metadata lost: %s", got["plan"])
	}
	before, _ := s.Entry(ctx, "record", "record")
	for _, body := range []string{`{"source":null}`, `{"id":"other"}`, `{"vehicleId":"other"}`, `{"kind":"service"}`, `{"title":null}`, `{"unknown":true}`, `{"plan":{"unknown":true}}`, `{"plan":null}`, `{"costCents":-1}`, `{}`} {
		w = request(h, "PATCH", "/api/v1/records/record", body)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("invalid patch %s: %d %s", body, w.Code, w.Body.String())
		}
		after, _ := s.Entry(ctx, "record", "record")
		if string(before) != string(after) {
			t.Fatal("invalid patch changed data")
		}
	}
}

func TestSourceProvenanceCannotBeForged(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/vehicles", `{"name":"Vehicle","source":{"system":"lubelogger"}}`},
		{"/api/v1/vehicles/" + v.ID + "/records", `{"kind":"note","title":"Undated note","source":{"system":"lubelogger"}}`},
		{"/api/v1/vehicles/" + v.ID + "/reminders", `{"title":"Reminder","dueDate":"2026-01-01","source":{"system":"lubelogger"}}`},
	} {
		w := request(h, "POST", tc.path, tc.body)
		if w.Code != 422 {
			t.Fatalf("forged source: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestReminderPatchRecurrenceAndNullableDue(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	w := request(h, "POST", "/api/v1/vehicles/"+v.ID+"/reminders", `{"title":"Oil","notes":"Preserve","dueOdometerMiles":1000,"recurrence":{"miles":500,"fixedIntervals":true},"tags":["engine"]}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var reminder garage.Reminder
	if err := json.Unmarshal(w.Body.Bytes(), &reminder); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/reminders/" + reminder.ID
	w = request(h, "PATCH", path, `{"completed":true}`)
	if w.Code != 422 {
		t.Fatalf("completion invented mileage: %d", w.Code)
	}
	w = request(h, "PATCH", path, `{"completed":true,"completionOdometerMiles":1100,"expectedDueOdometerMiles":1000}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reminder); err != nil {
		t.Fatal(err)
	}
	if reminder.Completed || reminder.DueOdometerMiles == nil || *reminder.DueOdometerMiles != 1500 || reminder.Notes != "Preserve" || len(reminder.Tags) != 1 {
		t.Fatalf("invalid advancement: %+v", reminder)
	}
	w = request(h, "PATCH", path, `{"completed":true,"completionOdometerMiles":1100,"expectedDueOdometerMiles":1000}`)
	if w.Code != 409 {
		t.Fatalf("replayed completion should conflict: %d %s", w.Code, w.Body.String())
	}
	w = request(h, "PATCH", path, `{"completed":true,"dueOdometerMiles":2000,"completionOdometerMiles":1100,"expectedDueOdometerMiles":1500}`)
	if w.Code != 422 {
		t.Fatalf("completion edited its anchor: %d %s", w.Code, w.Body.String())
	}
	w = request(h, "PATCH", path, `{"dueDate":"2026-11-01","dueOdometerMiles":null,"recurrence":null,"title":"One time"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	reminder = garage.Reminder{}
	if err := json.Unmarshal(w.Body.Bytes(), &reminder); err != nil {
		t.Fatal(err)
	}
	if reminder.DueOdometerMiles != nil || reminder.Recurrence != nil || reminder.Title != "One time" || reminder.Notes != "Preserve" {
		t.Fatalf("nullable edit failed: %+v", reminder)
	}
}

func TestConcurrentReminderPatchesPreserveDifferentFields(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	w := request(h, "POST", "/api/v1/vehicles/"+v.ID+"/reminders", `{"title":"Reminder","dueDate":"2026-10-08"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var reminder garage.Reminder
	if err := json.Unmarshal(w.Body.Bytes(), &reminder); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/reminders/" + reminder.ID
	entered, release := make(chan struct{}), make(chan struct{})
	body := &gatedReader{source: strings.NewReader(`{"title":"Changed"}`), entered: entered, release: release}
	r := httptest.NewRequest(http.MethodPatch, path, body)
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
	w = request(h, "PATCH", path, `{"notes":"Concurrent edit"}`)
	close(release)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	select {
	case w = <-result:
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked patch")
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reminder); err != nil {
		t.Fatal(err)
	}
	if reminder.Title != "Changed" || reminder.Notes != "Concurrent edit" {
		t.Fatalf("lost concurrent edit: %+v", reminder)
	}
}

func TestMetadataEditsDoNotLogPrivateFields(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	w := request(h, "POST", "/api/v1/vehicles/"+v.ID+"/records", `{"kind":"note","title":"Note"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var record garage.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h, err := New(s, testToken, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, "PATCH", "/api/v1/records/"+record.ID+"?private=private-query", `{"notes":"private-notes","tags":["private-tag"],"extraFields":[{"name":"private-field","value":"private-value","isRequired":false,"fieldType":0}]}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, secret := range []string{record.ID, v.ID, testToken, "private-query", "private-notes", "private-tag", "private-field", "private-value"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("metadata or identifiers appeared in request logs")
		}
	}
	if !strings.Contains(logs.String(), "PATCH /api/v1/records/{id}") {
		t.Fatal("route template missing")
	}
}
