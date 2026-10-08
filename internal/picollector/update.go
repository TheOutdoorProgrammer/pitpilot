package picollector

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const InstallDirectory = "/opt/pitpilot/picollector"
const UpdateDirectory = "/var/lib/pitpilot-updater"

var ErrRolledBack = errors.New("new collector failed health validation; prior binary restored")

type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	Version       string    `json:"version"`
	Sequence      uint64    `json:"sequence"`
	Platform      string    `json:"platform"`
	URL           string    `json:"url"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	ExpiresAt     time.Time `json:"expiresAt"`
}
type SignedManifest struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

func VersionSequence(version string) (uint64, error) {
	parts := stableVersion.FindStringSubmatch(version)
	if parts == nil {
		return 0, errors.New("stable version required")
	}
	var n uint64
	for _, p := range parts[1:] {
		v, _ := strconv.ParseUint(p, 10, 64)
		n = n*1000000 + v
	}
	return n, nil
}
func VerifyManifest(raw []byte, key ed25519.PublicKey, platform string, now time.Time) (Manifest, error) {
	var m Manifest
	var signed SignedManifest
	if len(raw) > 65536 || len(key) != ed25519.PublicKeySize || strictJSON(raw, &signed) != nil {
		return m, errors.New("invalid signed manifest")
	}
	payload, e1 := base64.StdEncoding.DecodeString(signed.Payload)
	signature, e2 := base64.StdEncoding.DecodeString(signed.Signature)
	if e1 != nil || e2 != nil || !ed25519.Verify(key, payload, signature) {
		return m, errors.New("update signature verification failed")
	}
	if strictJSON(payload, &m) != nil {
		return m, errors.New("invalid signed payload")
	}
	seq, err := VersionSequence(m.Version)
	digest, de := hex.DecodeString(m.SHA256)
	if err != nil || seq != m.Sequence || m.SchemaVersion != 1 || m.Platform != platform || m.Size < 1 || m.Size > 256<<20 || de != nil || len(digest) != sha256.Size || strings.ToLower(m.SHA256) != m.SHA256 || now.Year() < 2026 || !m.ExpiresAt.After(now) || m.ExpiresAt.After(now.Add(100*24*time.Hour)) {
		return m, errors.New("incompatible or expired update")
	}
	u, err := endpoint(m.URL, false)
	if err != nil {
		return m, err
	}
	// Each signed target names one immutable release, never latest/download.
	if !strings.Contains(u.Path, "/releases/download/v"+m.Version+"/") {
		return m, errors.New("update URL must pin the signed release")
	}
	return m, nil
}

type UpdateState struct {
	HighestSequence uint64 `json:"highestSequence"`
	HighestDigest   string `json:"highestDigest"`
	LastGood        string `json:"lastGood"`
	LastGoodVersion string `json:"lastGoodVersion"`
	Pending         string `json:"pending,omitempty"`
	FailedDigest    string `json:"failedDigest,omitempty"`
}
type ServiceManager interface {
	Stop(context.Context) error
	Start(context.Context) error
	Healthy(context.Context, string, time.Time) bool
}
type Updater struct {
	InstallDir string
	StateDir   string
	PublicKey  ed25519.PublicKey
	Platform   string
	Version    string
	Manager    ServiceManager
	Client     *http.Client
}

func NewUpdater(key, version string, c Config) (*Updater, error) {
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("release update public key unavailable")
	}
	return &Updater{InstallDir: InstallDirectory, StateDir: UpdateDirectory, PublicKey: decoded, Platform: runtime.GOOS + "/" + runtime.GOARCH, Version: version, Manager: systemService{c.StateDirectory}, Client: publicClient()}, nil
}
func publicClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		_, err := endpoint(r.URL.String(), false)
		return err
	}}
}
func (u *Updater) save(state UpdateState) error {
	raw, _ := json.Marshal(state)
	return AtomicFile(filepath.Join(u.StateDir, "state.json"), raw, 0600)
}
func (u *Updater) status(value string) {
	raw, _ := json.Marshal(struct {
		State string `json:"state"`
	}{value})
	_ = AtomicFile(filepath.Join(u.StateDir, "status.json"), raw, 0644)
}
func (u *Updater) targetOK(path string) bool {
	rel, err := filepath.Rel(filepath.Join(u.InstallDir, "releases"), path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.Base(path) != "picollector" {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0
}
func (u *Updater) current() (string, error) {
	p, err := os.Readlink(filepath.Join(u.InstallDir, "current"))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(u.InstallDir, p)
	}
	if !u.targetOK(p) {
		return "", errors.New("invalid installed release")
	}
	return p, nil
}
func (u *Updater) activate(target string) error {
	if !u.targetOK(target) {
		return errors.New("invalid activation target")
	}
	tmp := filepath.Join(u.InstallDir, ".current-pending")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Rename(tmp, filepath.Join(u.InstallDir, "current")); err != nil {
		return err
	}
	return syncDir(u.InstallDir)
}
func (u *Updater) rollback(ctx context.Context, s *UpdateState) error {
	if !u.targetOK(s.LastGood) {
		return errors.New("last known good release unavailable")
	}
	if err := u.Manager.Stop(ctx); err != nil {
		return err
	}
	if err := u.activate(s.LastGood); err != nil {
		return err
	}
	started := time.Now()
	if err := u.Manager.Start(ctx); err != nil {
		return err
	}
	if !u.Manager.Healthy(ctx, s.LastGoodVersion, started) {
		return errors.New("prior collector restart failed health validation")
	}
	s.FailedDigest = s.HighestDigest
	s.Pending = ""
	if err := u.save(*s); err != nil {
		return err
	}
	u.status("rolled_back")
	return nil
}

// Check recovers an interrupted activation before consulting remote update policy.
// Queue and identity files are deliberately outside the updater's install tree.
func (u *Updater) Check(ctx context.Context, c Config) (err error) {
	if u.Manager == nil || u.Client == nil {
		return errors.New("updater unavailable")
	}
	if err = os.MkdirAll(u.StateDir, 0755); err != nil {
		return err
	}
	lock, e := os.OpenFile(filepath.Join(u.StateDir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return errors.New("update already running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	defer func() {
		if err != nil && !errors.Is(err, ErrRolledBack) {
			u.status("failed")
		}
	}()
	var state UpdateState
	raw, e := os.ReadFile(filepath.Join(u.StateDir, "state.json"))
	if e == nil {
		if strictJSON(raw, &state) != nil {
			return errors.New("invalid updater state")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if state.Pending != "" {
		return u.rollback(ctx, &state)
	}
	current, e := u.current()
	if e != nil {
		return e
	}
	if state.LastGood == "" {
		seq, e := VersionSequence(u.Version)
		if e != nil {
			return e
		}
		binary, e := os.ReadFile(current)
		if e != nil {
			return e
		}
		digest := sha256.Sum256(binary)
		state.LastGood = current
		state.LastGoodVersion = u.Version
		state.HighestSequence = seq
		state.HighestDigest = hex.EncodeToString(digest[:])
		if e = u.save(state); e != nil {
			return e
		}
	}
	if current != state.LastGood {
		return errors.New("current executable differs from trusted update journal")
	}
	if !clockReady(c.TimeSyncMarker) {
		return errors.New("synchronized clock required for update")
	}
	id, e := ReadIdentity(c.StateDirectory)
	if e != nil {
		return e
	}
	api, e := NewClient(c.Server, id.Token, c.AllowLoopbackHTTP)
	if e != nil {
		return e
	}
	policy, e := api.Configuration(ctx)
	if e != nil {
		return e
	}
	if policy.DeviceID != id.DeviceID || policy.VehicleID != id.VehicleID {
		return errors.New("device identity mismatch")
	}
	if !policy.AutoUpdate {
		u.status("disabled")
		return nil
	}
	if _, e = endpoint(c.UpdateManifestURL, false); e != nil {
		return e
	}
	u.status("checking")
	raw, e = u.fetch(ctx, c.UpdateManifestURL, 65536)
	if e != nil {
		return e
	}
	m, e := VerifyManifest(raw, u.PublicKey, u.Platform, time.Now())
	if e != nil {
		return e
	}
	if m.Sequence < state.HighestSequence || (m.Sequence == state.HighestSequence && state.HighestDigest != "" && m.SHA256 != state.HighestDigest) {
		return errors.New("update rollback or equivocation refused")
	}
	if m.SHA256 == state.FailedDigest {
		u.status("rolled_back")
		return nil
	}
	if m.Sequence == state.HighestSequence && m.SHA256 == state.HighestDigest {
		u.status("healthy")
		return nil
	}
	u.status("downloading")
	target, e := u.download(ctx, m)
	if e != nil {
		return e
	}
	policy, e = api.Configuration(ctx)
	if e != nil {
		return e
	}
	if !policy.AutoUpdate {
		u.status("disabled")
		return nil
	}
	// Persist the recovery journal and anti-rollback watermark before changing the executable.
	state.HighestSequence = m.Sequence
	state.HighestDigest = m.SHA256
	state.LastGood = current
	state.Pending = target
	if e = u.save(state); e != nil {
		return e
	}
	u.status("staged")
	if e = u.Manager.Stop(ctx); e != nil {
		return e
	}
	activated := time.Now()
	u.status("applying")
	if e = u.activate(target); e == nil {
		e = u.Manager.Start(ctx)
	}
	if e == nil && u.Manager.Healthy(ctx, m.Version, activated) {
		state.LastGood = target
		state.LastGoodVersion = m.Version
		state.Pending = ""
		state.FailedDigest = ""
		if e = u.save(state); e != nil {
			return e
		}
		u.status("healthy")
		return nil
	}
	// Cancellation must not prevent restoration of the previous executable.
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	if e = u.rollback(recovery, &state); e != nil {
		return e
	}
	return ErrRolledBack
}
func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return nil, errors.New("invalid update request")
	}
	resp, e := u.Client.Do(r)
	if e != nil {
		return nil, errors.New("update download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("update unavailable")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil || int64(len(raw)) > limit {
		return nil, errors.New("update exceeds size limit")
	}
	return raw, nil
}
func (u *Updater) download(ctx context.Context, m Manifest) (string, error) {
	dir := filepath.Join(u.InstallDir, "releases", m.Version+"-"+m.SHA256[:16])
	if e := os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	target := filepath.Join(dir, "picollector")
	r, e := http.NewRequestWithContext(ctx, "GET", m.URL, nil)
	if e != nil {
		return "", errors.New("invalid target")
	}
	resp, e := u.Client.Do(r)
	if e != nil {
		return "", errors.New("target download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("target unavailable")
	}
	f, e := os.CreateTemp(dir, ".download-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, m.Size+1))
	if e != nil || n != m.Size || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return "", errors.New("target digest or size mismatch")
	}
	if e = f.Chmod(0755); e == nil {
		e = f.Sync()
	}
	if e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(f.Name(), target); e != nil {
		return "", e
	}
	if e = syncDir(dir); e != nil {
		return "", e
	}
	return target, nil
}

func (m Manifest) String() string { return fmt.Sprintf("collector %s (%s)", m.Version, m.Platform) }
