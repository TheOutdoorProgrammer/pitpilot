package picollector

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Server            string        `json:"server"`
	SerialPort        string        `json:"serialPort"`
	Baud              int           `json:"baud"`
	PollSeconds       int           `json:"pollSeconds"`
	QueueLimit        int           `json:"queueLimit"`
	StateDirectory    string        `json:"stateDirectory"`
	TimeSyncMarker    string        `json:"timeSyncMarker"`
	UpdateManifestURL string        `json:"updateManifestUrl"`
	AllowLoopbackHTTP bool          `json:"allowLoopbackHttp,omitempty"`
	Legacy            *LegacyConfig `json:"legacy,omitempty"`
}

func ReadConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, errors.New("configuration unavailable")
	}
	if len(raw) > 65536 || strictJSON(raw, &c) != nil {
		return c, errors.New("invalid configuration")
	}
	if _, err = endpoint(c.Server, c.AllowLoopbackHTTP); err != nil {
		return c, err
	}
	if c.PollSeconds < 5 || c.PollSeconds > 3600 || c.Baud < 1 || c.QueueLimit < 1 || c.QueueLimit > 1000000 || !filepath.IsAbs(c.StateDirectory) || !filepath.IsAbs(c.SerialPort) || !filepath.IsAbs(c.TimeSyncMarker) {
		return c, errors.New("invalid collector limits or paths")
	}
	if c.Legacy != nil {
		u, err := endpoint(c.Legacy.Endpoint, c.AllowLoopbackHTTP)
		if err != nil || u.Path != "/v1/obd/events" || !legacyIdentifier.MatchString(c.Legacy.DeviceID) || !filepath.IsAbs(c.Legacy.TokenFile) {
			return c, errors.New("invalid legacy receiver configuration")
		}
	}
	return c, nil
}

func endpoint(raw string, loopback bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid endpoint")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if !loopback || u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, errors.New("HTTPS required")
		}
	}
	return u, nil
}

func strictJSON(raw []byte, target any) error {
	if err := jsonutil.Validate(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func PrivateToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", errors.New("token file must be private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("token unavailable")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("invalid token")
	}
	return token, nil
}

// AtomicFile fsyncs both file and directory before reporting durable state.
func AtomicFile(path string, data []byte, mode os.FileMode) error {
	return atomicFileOwner(path, data, mode, -1, -1)
}
func atomicFileOwner(path string, data []byte, mode os.FileMode, uid, gid int) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if uid >= 0 {
		if err = f.Chown(uid, gid); err != nil {
			f.Close()
			return err
		}
	}
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
