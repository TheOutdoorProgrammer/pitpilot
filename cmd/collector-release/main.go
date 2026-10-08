// collector-release signs GoReleaser's collector output without exposing the
// signing key in command arguments or retaining it among release artifacts.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	Version       string    `json:"version"`
	Sequence      uint64    `json:"sequence"`
	Platform      string    `json:"platform"`
	URL           string    `json:"url"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "collector release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	f := flag.NewFlagSet("collector-release", flag.ContinueOnError)
	keyDir := f.String("generate-keys", "", "create new private signing/encryption keys in a new private directory")
	binary := f.String("binary", "", "GoReleaser-built binary path")
	arch := f.String("arch", "", "target architecture")
	version := f.String("version", "", "release version")
	public := f.String("public-key", "", "pinned base64 Ed25519 public key")
	snapshot := f.Bool("snapshot", false, "write a deliberately unsigned, non-installable snapshot manifest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *keyDir != "" {
		return generateKeys(*keyDir)
	}
	if *binary == "" || (*arch != "arm64" && *arch != "amd64") {
		return errors.New("binary and supported Linux architecture are required")
	}
	output := filepath.Join(filepath.Dir(*binary), "picollector_linux_"+*arch+".update.json")
	if *snapshot {
		return os.WriteFile(output, []byte("{\"snapshot\":true}\n"), 0644)
	}
	// Release candidates remain downloadable for manual trials, but must never
	// become an automatic fleet update through GitHub's release channel.
	if base, suffix, ok := strings.Cut(*version, "-"); ok && suffix != "" {
		if _, err := sequence(base); err != nil {
			return err
		}
		return os.WriteFile(output, []byte("{\"prerelease\":true}\n"), 0644)
	}
	seq, err := sequence(*version)
	if err != nil {
		return err
	}
	keyData, err := os.ReadFile(os.Getenv("PITPILOT_COLLECTOR_SIGNING_KEY_FILE"))
	if err != nil {
		return errors.New("cannot read release signing key")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyData)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid release signing key")
	}
	pinned, err := base64.StdEncoding.DecodeString(*public)
	if err != nil || len(pinned) != ed25519.PublicKeySize {
		return errors.New("invalid pinned public key")
	}
	if !ed25519.PublicKey(pinned).Equal(ed25519.PrivateKey(key).Public()) {
		return errors.New("signing key does not match collector trust root")
	}
	file, err := os.Open(*binary)
	if err != nil {
		return errors.New("cannot open collector binary")
	}
	defer file.Close()
	h := sha256.New()
	size, err := io.Copy(h, file)
	if err != nil || size == 0 || size > 256<<20 {
		return errors.New("invalid collector binary")
	}
	m := manifest{SchemaVersion: 1, Version: *version, Sequence: seq, Platform: "linux/" + *arch, URL: "https://github.com/TheOutdoorProgrammer/pitpilot/releases/download/v" + *version + "/picollector_linux_" + *arch, Size: size, SHA256: hex.EncodeToString(h.Sum(nil)), ExpiresAt: time.Now().UTC().Add(90 * 24 * time.Hour)}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(struct {
		Payload   []byte `json:"payload"`
		Signature []byte `json:"signature"`
	}{payload, ed25519.Sign(ed25519.PrivateKey(key), payload)})
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(envelope, '\n'), 0644)
}

func sequence(version string) (uint64, error) {
	parts := regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`).FindStringSubmatch(version)
	if len(parts) != 4 {
		return 0, errors.New("automatic updates require a stable semantic version")
	}
	var n uint64
	for _, part := range parts[1:] {
		value, _ := strconv.ParseUint(part, 10, 64)
		n = n*1_000_000 + value
	}
	return n, nil
}

func generateKeys(dir string) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return errors.New("key directory must not already exist")
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	var encryption [32]byte
	if _, err = rand.Read(encryption[:]); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"collector-signing-key": private, "collector-public-key": pub, "smartcar-encryption-key": encryption[:]} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(base64.StdEncoding.EncodeToString(data)), 0600); err != nil {
			return err
		}
	}
	return nil
}
