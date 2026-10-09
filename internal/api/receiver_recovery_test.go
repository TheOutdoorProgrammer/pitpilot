package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
)

type recoveryRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *recoveryRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func receiverAPIRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	w := &recoveryRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, r)
	return w.ResponseRecorder
}

func TestReceiverRecoveryAuthenticatedPreviewApply(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	body := garage.ReceiverRecoveryRequest{VehicleID: v.ID, Snapshot: receiverhistory.Snapshot{SHA256: strings.Repeat("a", 64), DeviceID: "receiver", Events: []json.RawMessage{json.RawMessage(`{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"observed_at":"2026-09-24T22:06:45.687965828Z","readings":{"speed_kph":25},"dtcs":[]}`)}}}
	raw, _ := json.Marshal(body)
	path := "/api/v1/migrations/receiver/"
	missing := body
	missing.VehicleID = "missing"
	missingRaw, _ := json.Marshal(missing)
	if w := receiverAPIRequest(h, "POST", path+"preview", string(missingRaw)); w.Code != 404 {
		t.Fatalf("missing vehicle: %d", w.Code)
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("POST", path+"preview", strings.NewReader(string(raw))))
	if unauth.Code != 401 {
		t.Fatal("recovery route bypasses auth")
	}
	if w := receiverAPIRequest(h, "POST", path+"apply", string(raw)); w.Code != 422 {
		t.Fatalf("apply without preview: %d", w.Code)
	}
	w := receiverAPIRequest(h, "POST", path+"preview", string(raw))
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var report garage.ReceiverRecoveryReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	body.PreviewToken = report.PreviewToken
	raw, _ = json.Marshal(body)
	w = receiverAPIRequest(h, "POST", path+"apply", string(raw))
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	if w = receiverAPIRequest(h, "POST", path+"apply", string(raw)); w.Code != 409 {
		t.Fatalf("stale preview token: %d", w.Code)
	}
	for _, invalid := range []string{`{"vehicleId":"a","vehicleId":"b"}`, string(raw) + string(raw), `{"unexpected":true}`, strings.Repeat(" ", receiverhistory.MaxRequestBytes+1)} {
		if w = receiverAPIRequest(h, "POST", path+"preview", invalid); w.Code != 400 {
			t.Fatalf("malformed/oversized: %d", w.Code)
		}
	}
}

type delayedRecoveryBody struct {
	io.ReadCloser
	delayed bool
}

func (body *delayedRecoveryBody) Read(p []byte) (int, error) {
	if !body.delayed {
		body.delayed = true
		time.Sleep(200 * time.Millisecond)
	}
	return body.ReadCloser.Read(p)
}

func TestReceiverRecoveryExtendsOnlyAuthenticatedResponseDeadline(t *testing.T) {
	_, h := fixture(t)
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/api/v1/migrations/receiver/preview", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+testToken)
		}
		w := &recoveryRecorder{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(w, r)
		if authenticated {
			if w.deadline.Before(time.Now().Add(119*time.Second)) || w.deadline.After(time.Now().Add(121*time.Second)) {
				t.Fatal("missing bounded recovery deadline")
			}
		} else if w.Code != 401 || !w.deadline.IsZero() {
			t.Fatal("unauthenticated request extended deadline")
		}
	}
	unsupported := request(h, "POST", "/api/v1/migrations/receiver/preview", `{}`)
	if unsupported.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported response controller: %d", unsupported.Code)
	}

	v := vehicle(t, h)
	body := garage.ReceiverRecoveryRequest{VehicleID: v.ID, Snapshot: receiverhistory.Snapshot{SHA256: strings.Repeat("a", 64), DeviceID: "receiver", Events: []json.RawMessage{json.RawMessage(`{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"observed_at":"2026-09-24T22:06:45.687965828Z","readings":{"speed_kph":25},"dtcs":[]}`)}}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &delayedRecoveryBody{ReadCloser: r.Body}
		h.ServeHTTP(w, r)
	}))
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Config.ReadTimeout = time.Second
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	for _, mode := range []string{"preview", "apply"} {
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest("POST", server.URL+"/api/v1/migrations/receiver/"+mode, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var report garage.ReceiverRecoveryReport
		err = json.NewDecoder(response.Body).Decode(&report)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || report.Applied != (mode == "apply") {
			t.Fatalf("recovery response lost past ordinary deadline: %d %v", response.StatusCode, err)
		}
		body.PreviewToken = report.PreviewToken
	}
	// The same delayed body on an ordinary endpoint still hits the server's
	// original deadline, proving the recovery allowance is route-scoped.
	req, err := http.NewRequest("POST", server.URL+"/api/v1/vehicles", strings.NewReader(`{"name":"Ordinary fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err == nil {
		response.Body.Close()
		t.Fatal("ordinary route bypassed its response deadline")
	}
}
