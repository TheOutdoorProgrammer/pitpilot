package picollector

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signedFixture(t *testing.T, m Manifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(SignedManifest{base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	return raw
}
func manifestFixture(binary []byte) Manifest {
	digest := sha256.Sum256(binary)
	seq, _ := VersionSequence("0.4.0")
	return Manifest{1, "0.4.0", seq, "linux/arm64", "https://downloads.example/releases/download/v0.4.0/picollector_linux_arm64", int64(len(binary)), hex.EncodeToString(digest[:]), time.Now().UTC().Add(90 * 24 * time.Hour)}
}
func TestManifestTrustPlatformExpiryAndStableVersion(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	m := manifestFixture([]byte("binary"))
	if _, err := VerifyManifest(signedFixture(t, m, key), pub, "linux/arm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Manifest){
		"platform":         func(m *Manifest) { m.Platform = "linux/amd64" },
		"expired":          func(m *Manifest) { m.ExpiresAt = time.Now().Add(-time.Second) },
		"unbounded-expiry": func(m *Manifest) { m.ExpiresAt = time.Now().Add(365 * 24 * time.Hour) },
		"development":      func(m *Manifest) { m.Version = "dev" },
		"sequence":         func(m *Manifest) { m.Sequence++ },
		"mutable-url":      func(m *Manifest) { m.URL = "https://downloads.example/releases/latest/download/binary" },
		"credential-url":   func(m *Manifest) { m.URL = "https://secret@downloads.example/releases/download/v0.4.0/binary" },
		"oversize":         func(m *Manifest) { m.Size = 257 << 20 },
	} {
		t.Run(name, func(t *testing.T) {
			v := m
			change(&v)
			if _, err := VerifyManifest(signedFixture(t, v, key), pub, "linux/arm64", time.Now()); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyManifest(signedFixture(t, m, other), pub, "linux/arm64", time.Now()); err == nil {
		t.Fatal("accepted untrusted signature")
	}
}

func TestPublicUpdateRedirectAllowsSignedQueryOnlyOverHTTPS(t *testing.T) {
	var origin string
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Error("public download sent credentials")
		}
		if r.URL.Path == "/manifest" {
			http.Redirect(w, r, origin+"/asset?sv=2026-01-01&sig=fixture%2Fsignature&expires=1799999999", http.StatusFound)
			return
		}
		if r.URL.Path != "/asset" || r.URL.Query().Get("sig") != "fixture/signature" {
			t.Error("signed query was lost")
		}
		_, _ = io.WriteString(w, "verified public fixture")
	}))
	defer server.Close()
	origin = server.URL
	client := publicClient()
	client.Transport = server.Client().Transport
	u := &Updater{Client: client}
	raw, err := u.fetch(context.Background(), origin+"/manifest", 128)
	if err != nil || string(raw) != "verified public fixture" || requests != 2 {
		t.Fatalf("signed redirect failed: requests=%d error=%v", requests, err)
	}
	if _, err := endpoint(origin+"/asset?sig=fixture", false); err == nil {
		t.Fatal("authenticated endpoint rules were relaxed")
	}
}

func TestPublicUpdateRedirectRejectsUnsafeDestinationsAndLoops(t *testing.T) {
	for _, name := range []string{"downgrade", "credentials", "fragment", "loop"} {
		t.Run(name, func(t *testing.T) {
			var location string
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer server.Close()
			switch name {
			case "downgrade":
				location = strings.Replace(server.URL, "https://", "http://", 1) + "/asset?sig=fixture"
			case "credentials":
				location = strings.Replace(server.URL, "https://", "https://user:fixture@", 1) + "/asset?sig=fixture"
			case "fragment":
				location = server.URL + "/asset?sig=fixture#fragment"
			case "loop":
				location = server.URL + "/again?sig=fixture"
			}
			client := publicClient()
			client.Transport = server.Client().Transport
			u := &Updater{Client: client}
			if _, err := u.fetch(context.Background(), server.URL, 128); err == nil {
				t.Fatal("unsafe redirect accepted")
			}
			if (name == "loop" && requests != 5) || (name != "loop" && requests != 1) {
				t.Fatalf("unexpected redirect request count %d", requests)
			}
		})
	}
}

func TestPublishedGitHubAssetDownload(t *testing.T) {
	if os.Getenv("PITPILOT_VERIFY_PUBLIC_ASSET") != "1" {
		t.Skip("explicit read-only public release verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	u := &Updater{Client: publicClient()}
	raw, err := u.fetch(ctx, "https://github.com/TheOutdoorProgrammer/pitpilot/releases/download/v0.3.1/checksums.txt", 64<<10)
	if err != nil || !bytes.Contains(raw, []byte("pitpilot_0.3.1_linux_arm64.tar.gz")) {
		t.Fatalf("published checksum download failed: %v", err)
	}
	t.Logf("downloaded published checksums through HTTPS release redirects (%d bytes)", len(raw))
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeService struct {
	stops, starts  int
	badVersion     string
	stopError      bool
	healthVersions []string
}

func (f *fakeService) Stop(context.Context) error {
	f.stops++
	if f.stopError {
		return errors.New("stop failed")
	}
	return nil
}
func (f *fakeService) Start(context.Context) error { f.starts++; return nil }
func (f *fakeService) Healthy(_ context.Context, v string, _ time.Time) bool {
	f.healthVersions = append(f.healthVersions, v)
	return v != f.badVersion
}

func TestPinnedUpdaterRollsBackLaterUpgradeToActualLastGoodVersion(t *testing.T) {
	f := newUpdateFixture(t)
	if err := f.u.Check(context.Background(), f.c); err != nil {
		t.Fatal(err)
	}
	good, _ := f.u.current()
	f.m.Version = "0.5.0"
	f.m.Sequence, _ = VersionSequence(f.m.Version)
	f.m.URL = "https://downloads.example/releases/download/v0.5.0/picollector_linux_arm64"
	f.binary = []byte("second candidate")
	digest := sha256.Sum256(f.binary)
	f.m.SHA256 = hex.EncodeToString(digest[:])
	f.m.Size = int64(len(f.binary))
	f.service.badVersion = "0.5.0"
	// u.Version remains the permanently installed bootstrap updater's 0.3.1.
	if !errors.Is(f.u.Check(context.Background(), f.c), ErrRolledBack) {
		t.Fatal("later failed upgrade did not roll back")
	}
	current, _ := f.u.current()
	s := f.state(t)
	if current != good || s.LastGoodVersion != "0.4.0" || f.service.healthVersions[len(f.service.healthVersions)-1] != "0.4.0" {
		t.Fatal("rollback used bootstrap version instead of installed last good", s, f.service.healthVersions)
	}
}

func TestSystemdRecoveryLauncherSurvivesUnexecutableCurrent(t *testing.T) {
	unit, err := os.ReadFile("../../deploy/picollector/picollector-update.service")
	if err != nil {
		t.Fatal(err)
	}
	var executable string
	for _, line := range strings.Split(string(unit), "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			executable = strings.Fields(strings.TrimPrefix(line, "ExecStart="))[0]
		}
	}
	if executable != InstallDirectory+"/updater" {
		t.Fatal("recovery depends on mutable current executable")
	}
	dir := t.TempDir()
	candidate := filepath.Join(dir, "broken")
	os.WriteFile(candidate, []byte("invalid executable format"), 0755)
	os.Symlink(candidate, filepath.Join(dir, "current"))
	if exec.Command(filepath.Join(dir, "current"), "update").Run() == nil {
		t.Fatal("fixture candidate unexpectedly starts")
	}
	launcher := filepath.Join(dir, filepath.Base(executable))
	os.WriteFile(launcher, []byte("#!/bin/sh\nprintf recovery-reached\n"), 0755)
	out, err := exec.Command(launcher, "update").Output()
	if err != nil || string(out) != "recovery-reached" {
		t.Fatal("independent recovery cannot start", err)
	}
	installer, err := os.ReadFile("../../scripts/picollector-install.sh")
	if err != nil || !strings.Contains(string(installer), `"$source_binary" /opt/pitpilot/picollector/updater`) {
		t.Fatal("bootstrap installer does not provision independent recovery")
	}
}

type updateFixture struct {
	u       *Updater
	c       Config
	m       Manifest
	key     ed25519.PrivateKey
	binary  []byte
	service *fakeService
	enabled bool
	fetches int
	old     string
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	f := &updateFixture{key: key, binary: []byte("new executable"), enabled: true, service: &fakeService{}}
	f.m = manifestFixture(f.binary)
	dir := t.TempDir()
	install := filepath.Join(dir, "install")
	state := filepath.Join(dir, "updater")
	data := filepath.Join(dir, "data")
	for _, p := range []string{filepath.Join(install, "releases", "bootstrap"), state, data} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.old = filepath.Join(install, "releases", "bootstrap", "picollector")
	os.WriteFile(f.old, []byte("old executable"), 0755)
	os.Symlink(f.old, filepath.Join(install, "current"))
	identity, _ := json.Marshal(Identity{DeviceID: "pi", VehicleID: "vehicle", Token: strings.Repeat("t", 32), ProtocolVersion: 1})
	os.WriteFile(filepath.Join(data, "identity.json"), identity, 0600)
	os.WriteFile(filepath.Join(data, "queue-sentinel"), []byte("never alter this queue"), 0600)
	marker := filepath.Join(dir, "synchronized")
	os.WriteFile(marker, nil, 0600)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DeviceConfig{DeviceID: "pi", VehicleID: "vehicle", AutoUpdate: f.enabled, ProtocolVersion: 1})
	}))
	t.Cleanup(s.Close)
	f.c = Config{Server: s.URL, StateDirectory: data, TimeSyncMarker: marker, UpdateManifestURL: "https://downloads.example/latest.json", AllowLoopbackHTTP: true}
	f.u = &Updater{InstallDir: install, StateDir: state, PublicKey: pub, Platform: "linux/arm64", Version: "0.3.1", Manager: f.service, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		f.fetches++
		raw := f.binary
		if r.URL.Path == "/latest.json" {
			raw = signedFixture(t, f.m, f.key)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
	})}}
	return f
}
func (f *updateFixture) state(t *testing.T) UpdateState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.u.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s UpdateState
	if strictJSON(raw, &s) != nil {
		t.Fatal("bad state")
	}
	return s
}
func TestUpdaterHealthyActivationAndIdempotency(t *testing.T) {
	f := newUpdateFixture(t)
	if err := f.u.Check(context.Background(), f.c); err != nil {
		t.Fatal(err)
	}
	s := f.state(t)
	target, err := f.u.current()
	if err != nil || target == f.old || s.Pending != "" || s.LastGood != target || s.HighestSequence != f.m.Sequence {
		t.Fatal(s, target, err)
	}
	if f.service.starts != 1 || f.service.stops != 1 {
		t.Fatal("incorrect service lifecycle")
	}
	if err = f.u.Check(context.Background(), f.c); err != nil {
		t.Fatal(err)
	}
	if f.service.starts != 1 {
		t.Fatal("reinstalled identical release")
	}
	raw, _ := os.ReadFile(filepath.Join(f.c.StateDirectory, "queue-sentinel"))
	if string(raw) != "never alter this queue" {
		t.Fatal("queue modified")
	}
}
func TestUpdaterHealthFailureRollsBackWithoutLoweringWatermark(t *testing.T) {
	f := newUpdateFixture(t)
	f.service.badVersion = "0.4.0"
	if f.u.Check(context.Background(), f.c) == nil {
		t.Fatal("unhealthy upgrade accepted")
	}
	s := f.state(t)
	current, _ := f.u.current()
	if current != f.old || s.Pending != "" || s.FailedDigest != f.m.SHA256 || s.HighestSequence != f.m.Sequence || s.LastGoodVersion != "0.3.1" {
		t.Fatal(s, current)
	}
	starts := f.service.starts
	status, _ := os.ReadFile(filepath.Join(f.u.StateDir, "status.json"))
	if string(status) != `{"state":"rolled_back"}` {
		t.Fatalf("rollback status overwritten: %s", status)
	}
	if err := f.u.Check(context.Background(), f.c); err != nil {
		t.Fatal(err)
	}
	if f.service.starts != starts {
		t.Fatal("repeated known-bad upgrade")
	}
}
func TestUpdaterRecoversInterruptedActivationBeforeNetwork(t *testing.T) {
	f := newUpdateFixture(t)
	f.service.stopError = true
	if f.u.Check(context.Background(), f.c) == nil {
		t.Fatal("expected interrupted activation")
	}
	if f.state(t).Pending == "" {
		t.Fatal("missing recovery journal")
	}
	f.service.stopError = false
	f.u.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("recovery contacted remote"); return nil, nil })
	if err := f.u.Check(context.Background(), f.c); err != nil {
		t.Fatal(err)
	}
	current, _ := f.u.current()
	if current != f.old || f.state(t).Pending != "" {
		t.Fatal("failed recovery")
	}
}
func TestUpdaterRejectsTamperingRollbackAndDisabledPolicy(t *testing.T) {
	t.Run("digest", func(t *testing.T) {
		f := newUpdateFixture(t)
		f.binary = []byte("tampered")
		if f.u.Check(context.Background(), f.c) == nil {
			t.Fatal("accepted bad target")
		}
		if f.service.stops != 0 {
			t.Fatal("stopped healthy collector before verification")
		}
	})
	t.Run("rollback", func(t *testing.T) {
		f := newUpdateFixture(t)
		f.m.Version = "0.3.0"
		f.m.Sequence, _ = VersionSequence(f.m.Version)
		f.m.URL = "https://downloads.example/releases/download/v0.3.0/binary"
		if f.u.Check(context.Background(), f.c) == nil {
			t.Fatal("accepted initial downgrade")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		f := newUpdateFixture(t)
		f.enabled = false
		if err := f.u.Check(context.Background(), f.c); err != nil {
			t.Fatal(err)
		}
		if f.fetches != 0 || f.service.stops != 0 {
			t.Fatal("ignored update policy")
		}
	})
	t.Run("clock", func(t *testing.T) {
		f := newUpdateFixture(t)
		os.Remove(f.c.TimeSyncMarker)
		if f.u.Check(context.Background(), f.c) == nil || f.fetches != 0 {
			t.Fatal("updated without trustworthy clock")
		}
	})
}
func TestHealthRejectsStaleWrongPIDAndWrongVersion(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Second)
	h := Health{"0.4.0", 123, started, time.Now(), true}
	write := func() { raw, _ := json.Marshal(h); os.WriteFile(filepath.Join(dir, "health.json"), raw, 0600) }
	write()
	if CheckHealth(dir, "0.4.0", 123, started.Add(-time.Second)) != nil {
		t.Fatal("healthy status rejected")
	}
	if CheckHealth(dir, "0.3.1", 123, started) == nil || CheckHealth(dir, "0.4.0", 124, started) == nil {
		t.Fatal("wrong process/version accepted")
	}
	h.CheckedAt = time.Now().Add(-time.Minute)
	write()
	if CheckHealth(dir, "0.4.0", 123, started) == nil {
		t.Fatal("stale health accepted")
	}
}
