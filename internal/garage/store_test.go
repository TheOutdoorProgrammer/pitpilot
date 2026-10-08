package garage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoppedDatabaseBackupRestoresRecords(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	s, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	v := Vehicle{ID: NewID(), Name: "Backup fixture", CreatedAt: time.Now().UTC()}
	if err = s.CreateVehicle(ctx, v); err != nil {
		t.Fatal(err)
	}
	r := Record{ID: NewID(), VehicleID: v.ID, Kind: "service", Title: "Service fixture", Date: "2026-10-08", CostCents: 1299}
	if err = s.SaveEntry(ctx, r.ID, v.ID, "record", r, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err = os.WriteFile(restored, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	export, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Vehicles) != 1 || len(export.Records) != 1 {
		t.Fatal("backup lost records")
	}
	got, err := s.Vehicle(ctx, v.ID)
	if err != nil || got.Name != v.Name {
		t.Fatalf("restored vehicle: %+v %v", got, err)
	}
}

func TestRejectsFutureSchema(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "future.db")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err = Open(filename); err == nil {
		s.Close()
		t.Fatal("opened newer schema")
	}
}
