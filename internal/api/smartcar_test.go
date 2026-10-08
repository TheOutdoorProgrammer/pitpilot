package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/smartcar"
)

func TestSmartcarRoutesAuthenticationSessionsAndPrivacy(t *testing.T) {
	store, disabled := fixture(t)
	v := vehicle(t, disabled)
	if w := request(disabled, "GET", "/api/v1/integrations/smartcar", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal("disabled status")
	}
	if w := request(disabled, "POST", "/api/v1/vehicles/"+v.ID+"/smartcar/sessions", `{"intent":"connect"}`); w.Code != 503 {
		t.Fatal("unconfigured connect enabled")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	svc, err := smartcar.New(store, smartcar.Config{ApplicationID: "11111111-2222-3333-4444-555555555555", ClientID: "client-fixture", ClientSecret: "private-client-secret-fixture", EncryptionKey: bytes.Repeat([]byte{7}, 32), Mode: "simulated", PollInterval: time.Hour}, logger)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewWithOptions(store, testToken, logger, Options{Smartcar: svc})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/vehicles/" + v.ID + "/smartcar"
	r := httptest.NewRequest("GET", base, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated integration readable")
	}
	r = httptest.NewRequest("POST", base+"/sessions", strings.NewReader(`{"intent":"connect"}`))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("wrong content type accepted")
	}
	for _, body := range []string{`{"intent":"connect","intent":"reconnect"}`, `{"Intent":"connect"}`, `null`, `{"intent":"connect"} {}`, `{"intent":null}`, `{"intent":"connect","secret":"private"}`} {
		if w := request(h, "POST", base+"/sessions", body); w.Code != 400 {
			t.Fatalf("invalid body accepted %s %d", body, w.Code)
		}
	}
	w = request(h, "POST", base+"/sessions", `{"intent":"connect"}`)
	if w.Code != 201 {
		t.Fatalf("begin %d %s", w.Code, w.Body)
	}
	var result smartcar.SessionResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse(result.AuthorizationURL)
	state := uri.Query().Get("state")
	if w = request(h, "POST", base+"/sessions/"+result.SessionID+"/complete", `{"state":"wrong","userId":"private-user-fixture"}`); w.Code != 409 {
		t.Fatalf("state replay %d", w.Code)
	}
	body, _ := json.Marshal(smartcar.Completion{State: state, Error: "private-oem-error"})
	if w = request(h, "POST", base+"/sessions/"+result.SessionID+"/complete", string(body)); w.Code != 400 || strings.Contains(w.Body.String(), "private-oem-error") {
		t.Fatal("callback error leaked")
	}
	w = request(h, "POST", base+"/sessions", `{"intent":"connect"}`)
	var cancelledSession smartcar.SessionResult
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &cancelledSession) != nil {
		t.Fatal("cancellation session missing")
	}
	cancelledURL, _ := url.Parse(cancelledSession.AuthorizationURL)
	body, _ = json.Marshal(smartcar.Completion{State: cancelledURL.Query().Get("state"), Error: "access_denied"})
	if w = request(h, "POST", base+"/sessions/"+cancelledSession.SessionID+"/complete", string(body)); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"cancelled"`) {
		t.Fatal("user cancellation not typed")
	}
	if w = request(h, "DELETE", base, ""); w.Code != 204 {
		t.Fatal("detach failed")
	}
	for _, secret := range []string{v.ID, result.SessionID, state, "private-client-secret-fixture", "private-user-fixture", "private-oem-error", "authorizationUrl"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("private integration detail entered operational logs")
		}
	}
}
