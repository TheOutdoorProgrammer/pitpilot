package picollector

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/diag"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/gps"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

type GPSConfig struct {
	SerialPort    string `json:"serialPort,omitempty"`
	DeviceGlob    string `json:"deviceGlob,omitempty"`
	Baud          int    `json:"baud"`
	SampleSeconds int    `json:"sampleSeconds"`
}

func (c GPSConfig) Validate() error {
	if (c.SerialPort == "") == (c.DeviceGlob == "") || c.Baud < 1200 || c.Baud > 115200 || c.SampleSeconds < 5 || c.SampleSeconds > 60 {
		return errors.New("GPS requires one serial path or by-id pattern, explicit baud and sampleSeconds between 5 and 60")
	}
	if c.SerialPort != "" && (!filepath.IsAbs(c.SerialPort) || !strings.HasPrefix(filepath.Clean(c.SerialPort), "/dev/") || strings.ContainsAny(c.SerialPort, "*?[]\x00\r\n")) {
		return errors.New("GPS serial path must be an explicit device path")
	}
	if c.DeviceGlob != "" && (!strings.HasPrefix(c.DeviceGlob, "/dev/serial/by-id/") || filepath.Dir(c.DeviceGlob) != "/dev/serial/by-id" || strings.ContainsAny(c.DeviceGlob, "\x00\r\n")) {
		return errors.New("GPS discovery is restricted to one by-id match")
	}
	if c.DeviceGlob != "" {
		if _, err := filepath.Match(c.DeviceGlob, ""); err != nil {
			return errors.New("invalid GPS by-id pattern")
		}
	}
	return nil
}

func gpsPath(c GPSConfig, obdPath string) (string, error) {
	path := c.SerialPort
	if c.DeviceGlob != "" {
		matches, err := filepath.Glob(c.DeviceGlob)
		if err != nil || len(matches) != 1 {
			return "", errors.New("GPS by-id match unavailable or ambiguous")
		}
		path = matches[0]
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("GPS device unavailable")
	}
	if obd, err := filepath.EvalSymlinks(obdPath); err == nil && obd == resolved {
		return "", errors.New("GPS and OBD devices overlap")
	}
	info, err := os.Stat(resolved)
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return "", errors.New("GPS path is not a serial device")
	}
	return resolved, nil
}

func collectGPS(ctx context.Context, c Config, q *Queue, state *runtimeState) {
	set := func(value string) {
		state.Lock()
		changed := state.gps != value
		state.gps = value
		state.Unlock()
		if changed {
			slog.InfoContext(ctx, "GPS collection state", "state", value)
		}
	}
	enabled := func() bool { state.Lock(); defer state.Unlock(); return state.gpsEnabled && !state.paused }
	probe := 0
	for ctx.Err() == nil {
		_, paused := state.state()
		if paused {
			set("paused")
			return
		}
		if !enabled() {
			set("disabled")
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		if !clockReady(c.TimeSyncMarker) {
			set("waiting_clock")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		configuration, err := gpsConfiguration(c.GPS, probe)
		if err != nil {
			set("disconnected")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		path, err := gpsPath(configuration, c.SerialPort)
		if err != nil {
			set("disconnected")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		port, err := serial.Open(path, &serial.Mode{BaudRate: configuration.Baud})
		if err != nil {
			set("disconnected")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if err = port.SetReadTimeout(time.Second); err != nil {
			port.Close()
			set("disconnected")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		set("waiting_fix")
		// The session identity changes on reconnect. No route is inferred across a
		// missing device or collector restart, even if the time gap was short.
		session := rand.Text()
		err = readGPS(ctx, port, configuration, session, func() bool { return clockReady(c.TimeSyncMarker) }, enabled, func(f gps.Fix, recording string) error {
			_, paused := state.state()
			if paused {
				return context.Canceled
			}
			batch := gpsBatch(f, recording)
			err := diag.Operation(ctx, "gps.queue.append", func(context.Context) error { return q.AppendGPS(batch) })
			if err != nil {
				set("queue_full")
			} else {
				set("fix")
			}
			return err
		}, set)
		port.Close()
		if errors.Is(err, errGPSNoSentences) {
			probe++
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			set("disconnected")
		}
		if !wait(ctx, 5*time.Second) {
			return
		}
	}
}

func gpsBatch(f gps.Fix, recording string) garage.SignalBatch {
	key := "gps:" + recording + ":" + f.RecordedAt.Format("150405.000000000")
	return garage.SignalBatch{Source: "pi", BatchID: key, Contexts: []garage.SignalContext{{Key: key, Kind: "location", ObservedAt: &f.RecordedAt, Location: &garage.SignalLocation{Latitude: f.Latitude, Longitude: f.Longitude, Type: "gps", RecordingID: recording, SpeedKPH: f.SpeedKPH, CourseDegrees: f.CourseDegrees, AltitudeMeters: f.AltitudeMeters, Satellites: &f.Satellites, HDOP: &f.HDOP, FixQuality: &f.Quality}}}}
}

func readGPS(ctx context.Context, port io.ReadCloser, c GPSConfig, session string, clockOK func() bool, enabled func() bool, accept func(gps.Fix, string) error, state func(string)) error {
	stop := context.AfterFunc(ctx, func() { port.Close() })
	defer stop()
	var parser gps.Parser
	var line []byte
	dropping := false
	buffer := make([]byte, 256)
	var last time.Time
	lastFix := time.Now()
	lastSentence := time.Now()
	for ctx.Err() == nil {
		if !enabled() {
			return nil
		}
		if time.Since(lastSentence) > 15*time.Second {
			return errGPSNoSentences
		}
		n, err := port.Read(buffer)
		if err != nil && n == 0 {
			return err
		}
		if !clockOK() {
			state("waiting_clock")
			parser = gps.Parser{}
			line = nil
			dropping = false
			last = time.Time{}
			if !wait(ctx, time.Second) {
				return ctx.Err()
			}
			continue
		}
		if time.Since(lastFix) > 30*time.Second {
			state("waiting_fix")
		}
		if n == 0 {
			if !wait(ctx, 10*time.Millisecond) {
				return ctx.Err()
			}
			continue
		}
		for _, b := range buffer[:n] {
			if b != '\n' {
				if !dropping {
					line = append(line, b)
					if len(line) > 256 {
						line = nil
						dropping = true
					}
				}
				continue
			}
			if dropping {
				dropping = false
				continue
			}
			if gps.ValidSentence(string(line)) {
				lastSentence = time.Now()
			}
			fix, _ := parser.Feed(string(line), time.Now().UTC())
			line = nil
			if fix == nil {
				continue
			}
			lastFix = time.Now()
			if !last.IsZero() && fix.RecordedAt.Sub(last) < time.Duration(c.SampleSeconds)*time.Second {
				continue
			}
			if err := accept(*fix, session+":"+fix.RecordedAt.Format(time.DateOnly)); err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				continue
			}
			last = fix.RecordedAt
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

var errGPSNoSentences = errors.New("GPS stream has no valid NMEA sentences")

func recognizedGPSPorts(ports []*enumerator.PortDetails) []string {
	var out []string
	for _, p := range ports {
		if p != nil && p.IsUSB && strings.EqualFold(p.VID, "1546") && (strings.EqualFold(p.PID, "01a6") || strings.EqualFold(p.PID, "01a7")) {
			out = append(out, p.Name)
		}
	}
	return out
}
func gpsConfiguration(config *GPSConfig, probe int) (GPSConfig, error) {
	if config != nil {
		return *config, nil
	}
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return GPSConfig{}, errors.New("GPS enumeration unavailable")
	}
	paths := recognizedGPSPorts(ports)
	if len(paths) != 1 {
		return GPSConfig{}, errors.New("GPS discovery unavailable or ambiguous")
	}
	bauds := []int{9600, 4800, 38400, 115200}
	return GPSConfig{SerialPort: paths[0], Baud: bauds[probe%len(bauds)], SampleSeconds: 5}, nil
}

type gpsPolicy struct {
	DeviceID string `json:"deviceId"`
	Enabled  bool   `json:"enabled"`
}

func readGPSPolicy(dir, device string) bool {
	path := filepath.Join(dir, "gps-policy.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
		return false
	}
	raw, err := os.ReadFile(path)
	var policy gpsPolicy
	return err == nil && strictJSON(raw, &policy) == nil && policy.DeviceID == device && policy.Enabled
}
func refreshGPSPolicy(ctx context.Context, dir, device string, client *Client, state *runtimeState) {
	for ctx.Err() == nil {
		config, err := client.Configuration(ctx)
		if err == nil && config.DeviceID == device {
			cached := readGPSPolicy(dir, device)
			state.Lock()
			changed := state.gpsEnabled != config.GPSRecording || cached != config.GPSRecording
			state.Unlock()
			if changed {
				raw, _ := json.Marshal(gpsPolicy{device, config.GPSRecording})
				persisted := AtomicFile(filepath.Join(dir, "gps-policy.json"), raw, 0600) == nil
				state.Lock()
				state.gpsEnabled = persisted && config.GPSRecording
				state.Unlock()
				if !persisted {
					slog.ErrorContext(ctx, "GPS policy could not be persisted; collection disabled")
				}
			}
		} else if err != nil {
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && (httpErr.Status == 401 || httpErr.Status == 403) {
				state.pause()
				return
			}
		}
		if !wait(ctx, 15*time.Second) {
			return
		}
	}
}
