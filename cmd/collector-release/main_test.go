package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSignedReleaseBindsArtifactAndTrustRoot(t *testing.T) {
	root := t.TempDir()
	keys := filepath.Join(root, "keys")
	if err := generateKeys(keys); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PITPILOT_COLLECTOR_SIGNING_KEY_FILE", filepath.Join(keys, "collector-signing-key"))
	public, err := os.ReadFile(filepath.Join(keys, "collector-public-key"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "picollector")
	if err = os.WriteFile(binary, []byte("synthetic executable"), 0755); err != nil {
		t.Fatal(err)
	}
	args := []string{"--binary", binary, "--arch", "arm64", "--version", "0.4.0", "--public-key", string(public)}
	if err = run(args); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "picollector_linux_arm64.update.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct{ Payload, Signature []byte }
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	pub, _ := base64.StdEncoding.DecodeString(string(public))
	if !ed25519.Verify(pub, envelope.Payload, envelope.Signature) {
		t.Fatal("signature invalid")
	}
	var m manifest
	if err = json.Unmarshal(envelope.Payload, &m); err != nil {
		t.Fatal(err)
	}
	if m.Platform != "linux/arm64" || m.Sequence != 4_000_000 || m.Size != 20 || m.Version != "0.4.0" {
		t.Fatal("incorrect payload", m)
	}
	other := filepath.Join(root, "other")
	if err = generateKeys(other); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PITPILOT_COLLECTOR_SIGNING_KEY_FILE", filepath.Join(other, "collector-signing-key"))
	if err = run(args); err == nil {
		t.Fatal("wrong signing key accepted")
	}
	for _, version := range []string{"0.4.0-SNAPSHOT", "0.4.0-rc1", "v0.4.0", "01.4.0", "1.1000000.0"} {
		if _, err = sequence(version); err == nil {
			t.Fatal("unstable/ambiguous version accepted")
		}
	}
}

func TestPrereleaseCannotProduceInstallableManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PITPILOT_COLLECTOR_SIGNING_KEY_FILE", "")
	if err := run([]string{"--binary", filepath.Join(root, "picollector"), "--arch", "arm64", "--version", "0.4.0-rc.1"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "picollector_linux_arm64.update.json"))
	if err != nil || string(raw) != "{\"prerelease\":true}\n" {
		t.Fatal("prerelease produced an installable manifest", err)
	}
}
