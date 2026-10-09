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
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
	bolt "go.etcd.io/bbolt"
)

func TestRecoverReceiverCLIRehearsal(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	directory := t.TempDir()
	input := filepath.Join(directory, "receiver.db")
	target := filepath.Join(directory, "garage.db")
	db, err := bolt.Open(input, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 24, 22, 6, 45, 687965828, time.UTC)
	e := receiverhistory.Event{SchemaVersion: 1, ID: "event", DeviceID: "receiver", BootID: "boot", Sequence: 1, ObservedAt: &at, Readings: map[string]float64{"speed_kph": 25}}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		metadata, err := tx.CreateBucket([]byte("metadata"))
		if err != nil {
			return err
		}
		if err = metadata.Put([]byte("device_id"), []byte(e.DeviceID)); err != nil {
			return err
		}
		events, err := tx.CreateBucket([]byte("events"))
		if err != nil {
			return err
		}
		return events.Put([]byte(e.ID), raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	sourceBefore, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	store, err := garage.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateVehicle(context.Background(), garage.Vehicle{ID: "vehicle", Name: "Fixture", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	args := []string{"--input", input, "--database", target, "--vehicle", "vehicle"}
	run := func(args []string) garage.ReceiverRecoveryReport {
		t.Helper()
		var output bytes.Buffer
		if err := recoverReceiver(args, &output); err != nil {
			t.Fatal(err)
		}
		var report garage.ReceiverRecoveryReport
		if err = json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	preview := run(args)
	if preview.Applied || preview.SamplesCreated != 1 {
		t.Fatal("invalid preview")
	}
	applied := run(append(append([]string{}, args...), "--apply", preview.PreviewToken))
	if !applied.Applied || applied.Archived != 1 {
		t.Fatal("invalid apply")
	}
	repeat := run(args)
	if repeat.Skipped != 1 || repeat.SamplesCreated != 0 {
		t.Fatal("repeat duplicated samples")
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(sourceBefore, after) {
		t.Fatal("CLI modified source")
	}
	var output bytes.Buffer
	if err = recoverReceiver([]string{"--input", input, "--database", input, "--vehicle", "vehicle"}, &output); err == nil {
		t.Fatal("accepted source as target")
	}
	after, _ = os.ReadFile(input)
	if !bytes.Equal(sourceBefore, after) {
		t.Fatal("same-file rejection modified source")
	}
}
