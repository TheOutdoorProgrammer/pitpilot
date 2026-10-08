package main

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/smartcar"
)

func configuredSmartcar(store *garage.Store, logger *slog.Logger) (*smartcar.Service, error) {
	if enabled := os.Getenv("PITPILOT_SMARTCAR_ENABLED"); enabled != "" {
		if enabled == "false" {
			return nil, nil
		}
		if enabled != "true" {
			return nil, errors.New("PITPILOT_SMARTCAR_ENABLED must be true or false")
		}
	}
	application := os.Getenv("PITPILOT_SMARTCAR_APPLICATION_ID")
	paths := []string{os.Getenv("PITPILOT_SMARTCAR_CLIENT_ID_FILE"), os.Getenv("PITPILOT_SMARTCAR_CLIENT_SECRET_FILE"), os.Getenv("PITPILOT_SMARTCAR_ENCRYPTION_KEY_FILE")}
	if application == "" && strings.Join(paths, "") == "" {
		if os.Getenv("PITPILOT_SMARTCAR_ENABLED") == "true" {
			return nil, errors.New("Smartcar is enabled without configuration")
		}
		return nil, nil
	}
	if application == "" {
		return nil, errors.New("Smartcar application ID is required when credentials are configured")
	}
	values := make([]string, len(paths))
	for i, path := range paths {
		if path == "" {
			return nil, errors.New("Smartcar requires client ID, client secret and encryption key files")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("cannot read Smartcar credential file")
		}
		values[i] = strings.TrimSpace(string(raw))
	}
	key, err := base64.StdEncoding.DecodeString(values[2])
	if err != nil || len(key) != 32 {
		return nil, errors.New("Smartcar encryption key must be 32 bytes encoded as base64")
	}
	interval := time.Hour
	if raw := os.Getenv("PITPILOT_SMARTCAR_POLL_INTERVAL"); raw != "" {
		interval, err = time.ParseDuration(raw)
		if err != nil {
			return nil, errors.New("invalid Smartcar poll interval")
		}
	}
	return smartcar.New(store, smartcar.Config{ApplicationID: application, ClientID: values[0], ClientSecret: values[1], EncryptionKey: key, Mode: env("PITPILOT_SMARTCAR_MODE", "live"), PollInterval: interval}, logger)
}
