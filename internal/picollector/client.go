package picollector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type Identity struct {
	DeviceID        string `json:"deviceId"`
	VehicleID       string `json:"vehicleId"`
	Token           string `json:"token"`
	AutoUpdate      bool   `json:"autoUpdate"`
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
}
type DeviceConfig struct {
	DeviceID        string `json:"deviceId"`
	VehicleID       string `json:"vehicleId"`
	AutoUpdate      bool   `json:"autoUpdate"`
	ProtocolVersion int    `json:"protocolVersion"`
}
type Heartbeat struct {
	Version         string     `json:"version"`
	QueuedBatches   int        `json:"queuedBatches"`
	CollectionState string     `json:"collectionState"`
	UpdateState     string     `json:"updateState"`
	RejectedSamples uint64     `json:"rejectedSamples"`
	LastObservedAt  *time.Time `json:"lastObservedAt,omitempty"`
	LastUploadAt    *time.Time `json:"lastUploadAt,omitempty"`
}
type HTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return "device request rejected" }

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

func NewClient(server, token string, loopback bool) (*Client, error) {
	u, err := endpoint(server, loopback)
	if err != nil {
		return nil, err
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("server must be an origin")
	}
	return &Client{u, token, &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, body, target any, expected int) error {
	return diag.Operation(ctx, "device"+path, func(ctx context.Context) error {
		var raw []byte
		var err error
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				return errors.New("invalid request")
			}
		}
		u := *c.base
		u.Path = path
		r, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(raw))
		if err != nil {
			return diag.NewError("request", nil)
		}
		r.Header.Set("Content-Type", "application/json")
		if c.token != "" {
			r.Header.Set("Authorization", "Bearer "+c.token)
		}
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(r.Header))
		resp, err := c.http.Do(r)
		if err != nil {
			return diag.NewError("network", nil)
		}
		defer resp.Body.Close()
		if resp.StatusCode != expected {
			retry := time.Duration(0)
			if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 {
				retry = min(time.Duration(n)*time.Second, time.Hour)
			}
			return &HTTPError{resp.StatusCode, retry}
		}
		if target == nil {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
		if err != nil || len(data) > 16384 || strictJSON(data, target) != nil {
			return diag.NewError("invalid_response", nil)
		}
		return nil
	})
}
func (c *Client) Configuration(ctx context.Context) (DeviceConfig, error) {
	var v DeviceConfig
	err := c.request(ctx, "GET", "/api/v1/device/config", nil, &v, 200)
	if err == nil && (v.DeviceID == "" || v.VehicleID == "" || v.ProtocolVersion != 1) {
		err = errors.New("unsupported device configuration")
	}
	return v, err
}
func (c *Client) Heartbeat(ctx context.Context, v Heartbeat) error {
	return c.request(ctx, "POST", "/api/v1/device/heartbeat", v, nil, 204)
}
func (c *Client) Upload(ctx context.Context, b garage.SignalBatch) error {
	var report struct {
		BatchID         string `json:"batchId"`
		Created         int    `json:"created"`
		Updated         int    `json:"updated"`
		Skipped         int    `json:"skipped"`
		ContextsCreated int    `json:"contextsCreated"`
		ContextsSkipped int    `json:"contextsSkipped"`
	}
	if err := c.request(ctx, "POST", "/api/v1/device/signals", b, &report, 200); err != nil {
		return err
	}
	if report.BatchID != b.BatchID || report.Created < 0 || report.Updated < 0 || report.Skipped < 0 || report.ContextsCreated < 0 || report.ContextsSkipped < 0 || report.Created+report.Updated+report.Skipped != len(b.Observations) || report.ContextsCreated+report.ContextsSkipped != len(b.Contexts) {
		return errors.New("incomplete acknowledgement")
	}
	return nil
}
func Enroll(ctx context.Context, c Config, tokenPath string) error {
	uid, gid := -1, -1
	if os.Geteuid() == 0 {
		if filepath.Clean(c.StateDirectory) != "/var/lib/pitpilot-collector" {
			return errors.New("privileged enrollment requires installed collector state directory")
		}
		u, err := user.Lookup("pitpilot-collector")
		if err != nil {
			return errors.New("install collector account before enrollment")
		}
		uid, err = strconv.Atoi(u.Uid)
		if err != nil || uid <= 0 {
			return errors.New("invalid collector account")
		}
		gid, err = strconv.Atoi(u.Gid)
		if err != nil || gid <= 0 {
			return errors.New("invalid collector group")
		}
	}
	if err := os.MkdirAll(c.StateDirectory, 0700); err != nil {
		return errors.New("state directory unavailable")
	}
	path := filepath.Join(c.StateDirectory, "identity.json")
	// Never overwrite a previous identity and strand its durable queue.
	lock, err := os.OpenFile(filepath.Join(c.StateDirectory, "enrollment.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("enrollment locked; inspect prior enrollment before retry")
	}
	lock.Close()
	defer os.Remove(filepath.Join(c.StateDirectory, "enrollment.lock"))
	if _, err = os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return errors.New("identity already exists or is unreadable")
	}
	token, err := PrivateToken(tokenPath)
	if err != nil {
		return err
	}
	client, err := NewClient(c.Server, "", c.AllowLoopbackHTTP)
	if err != nil {
		return err
	}
	var id Identity
	if err = client.request(ctx, "POST", "/api/v1/device/enroll", struct {
		Token string `json:"enrollmentToken"`
	}{token}, &id, 200); err != nil {
		return err
	}
	if id.DeviceID == "" || id.VehicleID == "" || len(id.Token) < 32 {
		return errors.New("invalid enrollment response")
	}
	raw, _ := json.Marshal(id)
	if atomicFileOwner(path, raw, 0600, uid, gid) != nil {
		return errors.New("identity could not be persisted; revoke this enrollment before retry")
	}
	return nil
}
func ReadIdentity(dir string) (Identity, error) {
	var id Identity
	path := filepath.Join(dir, "identity.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return id, errors.New("private identity unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil || strictJSON(raw, &id) != nil || id.DeviceID == "" || id.VehicleID == "" || len(id.Token) < 32 {
		return id, errors.New("invalid identity")
	}
	return id, nil
}
