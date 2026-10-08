package picollector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStrictACKAndNoRedirect(t *testing.T) {
	b := testBatch(t, "stable-batch")
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"complete", `{"batchId":"stable-batch","created":1,"updated":0,"skipped":0,"contextsCreated":3,"contextsSkipped":0}`, 200, true},
		{"retry", `{"batchId":"stable-batch","created":0,"updated":0,"skipped":1,"contextsCreated":0,"contextsSkipped":3}`, 200, true},
		{"missing", `{"batchId":"stable-batch","created":0,"updated":0,"skipped":0,"contextsCreated":3,"contextsSkipped":0}`, 200, false},
		{"wrong", `{"batchId":"other","created":1,"updated":0,"skipped":0,"contextsCreated":3,"contextsSkipped":0}`, 200, false},
		{"extra", `{"batchId":"stable-batch","created":1,"updated":0,"skipped":0,"contextsCreated":3,"contextsSkipped":0,"extra":1}`, 200, false},
		{"duplicate", `{"batchId":"wrong","batchId":"stable-batch","created":1,"updated":0,"skipped":0,"contextsCreated":3,"contextsSkipped":0}`, 200, false},
		{"unauthorized", "", 401, false}, {"redirect", "", 302, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing scoped token")
				}
				w.Header().Set("Location", "https://invalid.example")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c, _ := NewClient(s.URL, "fixture", true)
			err := c.Upload(context.Background(), b)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
func TestEndpointAndTokenValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?secret=x", "https://example.com#fragment", "http://localhost"} {
		if _, err := endpoint(raw, true); err == nil {
			t.Fatalf("accepted unsafe endpoint %s", raw)
		}
	}
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	os.WriteFile(token, []byte(strings.Repeat("x", 32)), 0644)
	if _, err := PrivateToken(token); err == nil {
		t.Fatal("accepted public token")
	}
	os.Chmod(token, 0600)
	if _, err := PrivateToken(token); err != nil {
		t.Fatal(err)
	}
	os.Symlink(token, filepath.Join(dir, "link"))
	if _, err := PrivateToken(filepath.Join(dir, "link")); err == nil {
		t.Fatal("accepted token symlink")
	}
}
func TestEnrollmentPersistsPrivateIdentityAndNeverOverwrites(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("enrollment unexpectedly authenticated")
		}
		json.NewEncoder(w).Encode(Identity{DeviceID: "device", VehicleID: "vehicle", Token: strings.Repeat("t", 32), ProtocolVersion: 1, AutoUpdate: true})
	}))
	defer s.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	os.WriteFile(token, []byte(strings.Repeat("e", 32)), 0600)
	c := Config{Server: s.URL, StateDirectory: dir, AllowLoopbackHTTP: true}
	if err := Enroll(context.Background(), c, token); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIdentity(dir); err != nil {
		t.Fatal(err)
	}
	if Enroll(context.Background(), c, token) == nil || calls != 1 {
		t.Fatal("overwrote existing enrollment")
	}
}
func TestLegacyACKRequiresExactEvent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"accepted":["different"]}`) }))
	defer s.Close()
	c, _ := NewClient(s.URL, "fixture", true)
	if c.UploadLegacy(context.Background(), LegacyEvent{ID: "sample"}) == nil {
		t.Fatal("accepted unrelated legacy acknowledgement")
	}
}
func TestRevocationRetainsQueueAndPausesCollection(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer s.Close()
	c, _ := NewClient(s.URL, "fixture", true)
	q, err := OpenQueue(t.TempDir(), "device", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	q.Append(testBatch(t, "queued"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state := &runtimeState{}
	upload(ctx, q, c, state)
	_, paused := state.state()
	if !paused {
		t.Fatal("revocation did not pause reader")
	}
	if n, _, _ := q.Status(); n != 1 {
		t.Fatal("revocation discarded queue")
	}
}
