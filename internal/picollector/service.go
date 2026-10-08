package picollector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type systemService struct{ stateDirectory string }

func (systemService) Stop(ctx context.Context) error  { return serviceCommand(ctx, "stop") }
func (systemService) Start(ctx context.Context) error { return serviceCommand(ctx, "start") }
func serviceCommand(ctx context.Context, verb string) error {
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", verb, "picollector.service")
	if command.Run() != nil {
		return errors.New("collector service action failed")
	}
	return nil
}
func (s systemService) Healthy(ctx context.Context, version string, after time.Time) bool {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	stableSince := time.Time{}
	lastPID := 0
	for ctx.Err() == nil {
		pidRaw, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", "picollector.service").Output()
		pid, e := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
		if err == nil && e == nil && pid > 0 && CheckHealth(s.stateDirectory, version, pid, after) == nil {
			if lastPID != pid {
				stableSince = time.Now()
				lastPID = pid
			}
			if time.Since(stableSince) >= 20*time.Second {
				return true
			}
		} else {
			stableSince = time.Time{}
			lastPID = 0
		}
		if !wait(ctx, 2*time.Second) {
			break
		}
	}
	return false
}
func CheckHealth(dir, version string, pid int, after time.Time) error {
	raw, err := os.ReadFile(filepath.Join(dir, "health.json"))
	var h Health
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &h) != nil || h.PID != pid || h.Version != version || !h.QueueReady || h.StartedAt.Before(after) || h.CheckedAt.Before(h.StartedAt) || time.Since(h.CheckedAt) < 0 || time.Since(h.CheckedAt) > 35*time.Second {
		return errors.New("collector not healthy")
	}
	return nil
}
