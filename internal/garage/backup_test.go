package garage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func oldBackupFixture(t *testing.T) (string, *sql.DB, Vehicle, Record) {
	t.Helper()
	// URI-significant characters catch accidental filename interpolation into DSNs.
	source := filepath.Join(t.TempDir(), "old ? garage#.db")
	db, err := sql.Open("sqlite", sqliteFileURI(source, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
		CREATE TABLE vehicles(id TEXT PRIMARY KEY, data TEXT NOT NULL CHECK(json_valid(data)));
		CREATE TABLE entries(id TEXT PRIMARY KEY, vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, kind TEXT NOT NULL CHECK(kind IN ('record','reminder','trip')), data TEXT NOT NULL CHECK(json_valid(data)));
		CREATE INDEX entries_vehicle_kind ON entries(vehicle_id,kind);
		PRAGMA user_version=1;
		PRAGMA wal_checkpoint(TRUNCATE);`)
	if err != nil {
		t.Fatal(err)
	}
	v := Vehicle{ID: "backup-vehicle", Name: "Synthetic vehicle", CreatedAt: time.Now().UTC()}
	r := Record{ID: "backup-record", VehicleID: v.ID, Kind: "service", Title: "Synthetic service", Date: "2026-10-08", CostCents: 1299}
	vehicle, _ := json.Marshal(v)
	record, _ := json.Marshal(r)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO vehicles VALUES (?, ?)", v.ID, string(vehicle)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO entries VALUES (?, ?, ?, ?)", r.ID, v.ID, "record", string(record)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return source, db, v, r
}

func backupFileHash(t *testing.T, file string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestBackupPreservesOldSchemaAndLiveWAL(t *testing.T) {
	ctx := context.Background()
	source, live, vehicle, record := oldBackupFixture(t)
	wal, err := os.Stat(source + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatal("fixture must have committed data in WAL", err)
	}
	beforeDB, beforeWAL := backupFileHash(t, source), backupFileHash(t, source+"-wal")
	destination := filepath.Join(t.TempDir(), "snapshot ?#.db")
	if err = Backup(ctx, source, destination); err != nil {
		t.Fatal(err)
	}
	if backupFileHash(t, source) != beforeDB || backupFileHash(t, source+"-wal") != beforeWAL {
		t.Fatal("backup modified source database or WAL")
	}
	var sourceVersion int
	if err = live.QueryRow("PRAGMA user_version").Scan(&sourceVersion); err != nil || sourceVersion != 1 {
		t.Fatal("backup migrated the source", sourceVersion, err)
	}
	copyDB, err := sql.Open("sqlite", sqliteFileURI(destination, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	var version, count int
	if err = copyDB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatal("backup must preserve schema 1", version, err)
	}
	if err = copyDB.QueryRow("SELECT COUNT(*) FROM entries").Scan(&count); err != nil || count != 1 {
		t.Fatal("backup omitted committed WAL data", count, err)
	}
	if err = copyDB.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup must be private", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if err = os.WriteFile(restoredPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Vehicle(ctx, vehicle.ID)
	if err != nil || got.Name != vehicle.Name {
		t.Fatal("restored vehicle differs", err)
	}
	export, err := restored.Export(ctx)
	if err != nil || len(export.Records) != 1 {
		t.Fatal("restored record missing", err)
	}
	var gotRecord Record
	if err = json.Unmarshal(export.Records[0], &gotRecord); err != nil || gotRecord.CostCents != record.CostCents || gotRecord.ID != record.ID {
		t.Fatal("restored record differs", err)
	}
}

func TestBackupRefusesExistingAndSourceJournalDestinations(t *testing.T) {
	source, _, _, _ := oldBackupFixture(t)
	destination := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(destination, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	before := backupFileHash(t, destination)
	for _, output := range []string{source, source + "-wal", source + "-shm", source + "-journal", destination} {
		if err := Backup(context.Background(), source, output); err == nil {
			t.Fatal("overwrote a reserved or existing destination")
		}
	}
	if backupFileHash(t, destination) != before {
		t.Fatal("existing destination was changed")
	}
}

func TestBackupCancellationAndFailureLeaveNoOutput(t *testing.T) {
	source, _, _, _ := oldBackupFixture(t)
	directory := t.TempDir()
	destination := filepath.Join(directory, "cancelled.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Backup(ctx, source, destination); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation", err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Backup(context.Background(), corrupt, destination); err == nil {
		t.Fatal("accepted corrupt database")
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("failed backup left an output or recovery workspace", err)
	}
}

func TestConcurrentBackupsNeverOverwriteDestination(t *testing.T) {
	source, _, _, _ := oldBackupFixture(t)
	destination := filepath.Join(t.TempDir(), "one-winner.db")
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- Backup(context.Background(), source, destination)
		}()
	}
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("got %d published backups, want one", winners)
	}
	if err := verifySQLiteBackup(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
}
