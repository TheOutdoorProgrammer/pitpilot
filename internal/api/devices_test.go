package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func deviceAPIRequest(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDeviceAPICannotUseHouseholdAuthority(t *testing.T) {
	_, handler := fixture(t)
	v := vehicle(t, handler)
	w := request(handler, "POST", "/api/v1/vehicles/"+v.ID+"/devices", `{"name":"Synthetic Pi"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var e garage.DeviceEnrollment
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"enrollmentToken": e.EnrollmentToken})
	w = deviceAPIRequest(handler, "POST", "/api/v1/device/enroll", "", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var device garage.EnrolledDevice
	if err := json.Unmarshal(w.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, token, body string
		want                      int
	}{
		{"GET", "/api/v1/vehicles", device.Token, "", 401},
		{"POST", "/api/v1/vehicles/" + v.ID + "/devices", device.Token, `{"name":"escape"}`, 401},
		{"GET", "/api/v1/device/config", testToken, "", 401},
		{"GET", "/api/v1/device/config", e.EnrollmentToken, "", 401},
		{"GET", "/api/v1/device/config", device.Token, "", 200},
		{"POST", "/api/v1/device/enroll", "", string(raw), 401},
		{"PATCH", "/api/v1/devices/" + device.DeviceID, testToken, `{}`, 422},
		{"PATCH", "/api/v1/devices/" + device.DeviceID, testToken, `{"autoUpdate":false}`, 200},
		{"POST", "/api/v1/device/heartbeat", device.Token, `{"version":"0.4.0","queuedBatches":1,"rejectedSamples":0,"collectionState":"waiting_clock","updateState":"disabled"}`, 204},
		{"POST", "/api/v1/device/heartbeat", device.Token, `{"version":"0.4.0","queuedBatches":-1,"collectionState":"collecting","updateState":"idle"}`, 422},
	} {
		w := deviceAPIRequest(handler, tc.method, tc.path, tc.token, tc.body)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d want%d", tc.method, tc.path, w.Code, tc.want)
		}
	}
	at := time.Now().UTC()
	batch := garage.SignalBatch{Source: "smartcar", BatchID: "private-batch", Observations: []garage.SignalObservation{{Key: "private-key", Metric: "manifold_kpa", Unit: "kPa", Statistic: "sample", Quality: "measured", Value: 42, ObservedAt: &at}}}
	raw, _ = json.Marshal(batch)
	for range 2 {
		w = deviceAPIRequest(handler, "POST", "/api/v1/device/signals", device.Token, string(raw))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"batchId":"private-batch"`) {
			t.Fatal(w.Code, w.Body)
		}
	}
	w = request(handler, "DELETE", "/api/v1/devices/"+device.DeviceID, "")
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	w = deviceAPIRequest(handler, "POST", "/api/v1/device/signals", device.Token, string(raw))
	if w.Code != 401 {
		t.Fatal("revoked token accepted", w.Code)
	}
}
