package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestSmartcarCanRemainDisabledUntilCredentialsAreReady(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("PITPILOT_SMARTCAR_ENABLED", "false")
	t.Setenv("PITPILOT_SMARTCAR_APPLICATION_ID", "")
	t.Setenv("PITPILOT_SMARTCAR_CLIENT_ID_FILE", "/missing/client-id")
	t.Setenv("PITPILOT_SMARTCAR_CLIENT_SECRET_FILE", "/missing/client-secret")
	t.Setenv("PITPILOT_SMARTCAR_ENCRYPTION_KEY_FILE", "/missing/key")
	if service, err := configuredSmartcar(nil, logger); err != nil || service != nil {
		t.Fatal("explicit disabled setup blocked garage", err)
	}
	t.Setenv("PITPILOT_SMARTCAR_ENABLED", "true")
	if _, err := configuredSmartcar(nil, logger); err == nil {
		t.Fatal("partial enabled setup accepted")
	}
	for _, name := range []string{"PITPILOT_SMARTCAR_CLIENT_ID_FILE", "PITPILOT_SMARTCAR_CLIENT_SECRET_FILE", "PITPILOT_SMARTCAR_ENCRYPTION_KEY_FILE"} {
		t.Setenv(name, "")
	}
	if _, err := configuredSmartcar(nil, logger); err == nil {
		t.Fatal("enabled setup silently disabled without configuration")
	}
	t.Setenv("PITPILOT_SMARTCAR_ENABLED", "tru")
	if _, err := configuredSmartcar(nil, logger); err == nil {
		t.Fatal("invalid enable flag ignored")
	}
}
