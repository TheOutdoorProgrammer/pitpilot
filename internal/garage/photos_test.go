package garage

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"path/filepath"
	"testing"
	"time"
)

func TestVehiclePhotoSurvivesOnlineBackup(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	s, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := Vehicle{ID: NewID(), Name: "Photo backup", CreatedAt: time.Now().UTC()}
	if err = s.CreateVehicle(ctx, v); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err = jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 20, 12)), nil); err != nil {
		t.Fatal(err)
	}
	saved, err := s.PutVehiclePhoto(ctx, v.ID, encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err = Backup(ctx, source, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	p, err := restored.VehiclePhoto(ctx, v.ID)
	if err != nil || p.Revision != saved.PhotoRevision {
		t.Fatal("backup lost photo", err)
	}
	got, err := restored.Vehicle(ctx, v.ID)
	if err != nil || got.PhotoRevision != p.Revision {
		t.Fatal("backup metadata disagrees", err)
	}
	ex, err := restored.Export(ctx)
	if err != nil || len(ex.VehiclePhotos) != 1 || !bytes.Equal(ex.VehiclePhotos[0].Data, p.Data) {
		t.Fatal("restored export lost photo", err)
	}
}
