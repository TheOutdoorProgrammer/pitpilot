package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/smartcar"
)

func TestWebhookPublicBoundarySignatureAndPrivacy(t *testing.T) {
	store, disabled := fixture(t)
	path := "/api/v1/integrations/smartcar/webhook"
	if w := request(disabled, "POST", path, `{}`); w.Code != 503 {
		t.Fatal("unconfigured receiver accepted event")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	key := "private-management-token-fixture"
	svc, err := smartcar.New(store, smartcar.Config{ApplicationID: "11111111-2222-3333-4444-555555555555", ClientID: "client-fixture", ClientSecret: "private-client-secret-fixture", EncryptionKey: bytes.Repeat([]byte{7}, 32), Mode: "simulated", ManagementToken: key, WebhookID: "11111111-2222-3333-4444-555555555555"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewWithOptions(store, testToken, logger, Options{Smartcar: svc})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"eventId":"event-fixture","eventType":"VERIFY","data":{"challenge":"random-challenge-fixture-123"},"meta":{"version":"4.0","webhookId":"11111111-2222-3333-4444-555555555555","deliveryId":"delivery-fixture","deliveredAt":"2026-10-10T12:00:00Z"}}`
	call := func(body, contentType, signature string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		if signature != "" {
			r.Header.Set("SC-Signature", signature)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call(body, "application/json", "")
	if w.Code != 200 {
		t.Fatal("unsigned provider verification failed", w.Code, w.Body)
	}
	var result map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte("random-challenge-fixture-123"))
	if result["challenge"] != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("incorrect challenge response")
	}
	state := `{"eventId":"event-fixture","eventType":"VEHICLE_STATE","data":{"user":{"id":"private-user-fixture"},"vehicle":{"id":"private-vehicle-fixture","mode":"simulated"},"signals":[]},"meta":{"version":"4.0","webhookId":"11111111-2222-3333-4444-555555555555","deliveryId":"delivery-fixture","deliveredAt":1791633600000,"mode":"TEST"}}`
	if w = call(state, "application/json", ""); w.Code != 401 {
		t.Fatal("unsigned data accepted", w.Code)
	}
	mac = hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(state))
	sig := hex.EncodeToString(mac.Sum(nil))
	if w = call(state, "application/json", sig); w.Code != 200 || !strings.Contains(w.Body.String(), "ignored") {
		t.Fatal("unknown binding not ignored", w.Code)
	}
	if w = call(state+" ", "application/json", sig); w.Code != 401 {
		t.Fatal("changed signed body accepted")
	}
	if w = call(body, "text/plain", ""); w.Code != 415 {
		t.Fatal("wrong media type accepted")
	}
	if w = call(strings.Repeat("x", (1<<20)+1), "application/json", ""); w.Code != 413 {
		t.Fatal("oversized body accepted")
	}
	r := httptest.NewRequest("GET", "/api/v1/vehicles", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("public receiver bypassed garage authentication")
	}
	for _, secret := range []string{key, "private-user-fixture", "private-vehicle-fixture", "random-challenge-fixture-123", sig} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("webhook private value leaked to logs")
		}
	}
}
