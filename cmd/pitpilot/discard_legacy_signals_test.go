package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestDiscardLegacySignalsCLI(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	filename := filepath.Join(t.TempDir(), "garage.db")
	store, err := garage.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	if err = store.CreateVehicle(context.Background(), garage.Vehicle{ID: "vehicle", Name: "Fixture", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	batch := garage.SignalBatch{Source: "lubelogger", BatchID: "legacy", Observations: []garage.SignalObservation{{Key: "sample", Metric: "speed_kph", Unit: "km/h", Statistic: "sample", Quality: "measured", ObservedAt: &at, Value: 25}}}
	if _, err = store.IngestSignals(context.Background(), "vehicle", batch); err != nil {
		t.Fatal(err)
	}
	store.Close()
	args := []string{"--database", filename}
	run := func(args []string) garage.LegacySignalDiscardReport {
		t.Helper()
		var output bytes.Buffer
		if err := discardLegacySignals(args, &output); err != nil {
			t.Fatal(err)
		}
		var report garage.LegacySignalDiscardReport
		if err = json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	preview := run(args)
	if preview.Applied || preview.LubeLoggerSamplesDeleted != 1 || preview.PoliciesCreated != 1 {
		t.Fatal("wrong CLI preview")
	}
	applied := run(append(append([]string{}, args...), "--apply", preview.PreviewToken))
	if !applied.Applied || applied.LubeLoggerSamplesDeleted != 1 {
		t.Fatal("wrong CLI apply")
	}
	repeat := run(args)
	if repeat.LubeLoggerSamplesDeleted != 0 || repeat.PoliciesCreated != 0 {
		t.Fatal("CLI repeat has work")
	}
	missing := filepath.Join(t.TempDir(), "absent.db")
	var output bytes.Buffer
	if err = discardLegacySignals([]string{"--database", missing}, &output); err == nil {
		t.Fatal("accepted missing database")
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("created missing database")
	}
}
