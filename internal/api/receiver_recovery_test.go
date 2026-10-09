package api

import (
	"encoding/json"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReceiverRecoveryAuthenticatedPreviewApply(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	body := garage.ReceiverRecoveryRequest{VehicleID: v.ID, Snapshot: receiverhistory.Snapshot{SHA256: strings.Repeat("a", 64), DeviceID: "receiver", Events: []json.RawMessage{json.RawMessage(`{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"observed_at":"2026-09-24T22:06:45.687965828Z","readings":{"speed_kph":25},"dtcs":[]}`)}}}
	raw, _ := json.Marshal(body)
	path := "/api/v1/migrations/receiver/"
	missing := body
	missing.VehicleID = "missing"
	missingRaw, _ := json.Marshal(missing)
	if w := request(h, "POST", path+"preview", string(missingRaw)); w.Code != 404 {
		t.Fatalf("missing vehicle: %d", w.Code)
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("POST", path+"preview", strings.NewReader(string(raw))))
	if unauth.Code != 401 {
		t.Fatal("recovery route bypasses auth")
	}
	if w := request(h, "POST", path+"apply", string(raw)); w.Code != 422 {
		t.Fatalf("apply without preview: %d", w.Code)
	}
	w := request(h, "POST", path+"preview", string(raw))
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var report garage.ReceiverRecoveryReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	body.PreviewToken = report.PreviewToken
	raw, _ = json.Marshal(body)
	w = request(h, "POST", path+"apply", string(raw))
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	if w = request(h, "POST", path+"apply", string(raw)); w.Code != 409 {
		t.Fatalf("stale preview token: %d", w.Code)
	}
	for _, invalid := range []string{`{"vehicleId":"a","vehicleId":"b"}`, string(raw) + string(raw), `{"unexpected":true}`, strings.Repeat(" ", receiverhistory.MaxRequestBytes+1)} {
		if w = request(h, "POST", path+"preview", invalid); w.Code != 400 {
			t.Fatalf("malformed/oversized: %d", w.Code)
		}
	}
}
