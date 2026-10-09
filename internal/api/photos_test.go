package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, width, height))
	im.Set(0, 0, color.RGBA{255, 128, 0, 255})
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func photoRequest(h http.Handler, method, path, contentType, token string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestVehiclePhotoLifecycleAndPrivacy(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	path := "/api/v1/vehicles/" + v.ID + "/photo"
	jpegData := testJPEG(t, 12, 8)
	// An APP1 segment stands in for EXIF metadata. Both it and a trailing
	// payload must disappear even when a client skips native sanitation.
	private := []byte("GPSLocation=private-synthetic")
	upload := append([]byte{}, jpegData[:2]...)
	upload = append(upload, 0xff, 0xe1, 0, byte(len(private)+2))
	upload = append(upload, private...)
	upload = append(upload, jpegData[2:]...)
	upload = append(upload, private...)
	w := photoRequest(h, "PUT", path, "image/jpeg", testToken, upload)
	if w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	var saved garage.Vehicle
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.PhotoRevision) != 64 || saved.OdometerMiles != v.OdometerMiles {
		t.Fatal("missing revision or changed mileage")
	}
	w = request(h, "GET", path, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("photo headers/status %d %v", w.Code, w.Header())
	}
	if bytes.Contains(w.Body.Bytes(), private) {
		t.Fatal("private metadata survived")
	}
	if _, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	canonical := append([]byte(nil), w.Body.Bytes()...)
	if w.Header().Get("ETag") != `"`+saved.PhotoRevision+`"` {
		t.Fatal("revision mismatch")
	}
	ex, err := s.Export(context.Background())
	if err != nil || len(ex.VehiclePhotos) != 1 || !bytes.Equal(ex.VehiclePhotos[0].Data, canonical) {
		t.Fatal("export lost photo", err)
	}
	w = request(h, "PATCH", "/api/v1/vehicles/"+v.ID, `{"name":"Renamed"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), saved.PhotoRevision) {
		t.Fatal("metadata edit lost photo")
	}
	for i := 0; i < 2; i++ {
		w = request(h, "DELETE", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "photoRevision") {
			t.Fatal("delete must clear revision idempotently")
		}
	}
	if w = request(h, "GET", path, ""); w.Code != 404 {
		t.Fatal("deleted photo still served")
	}
	if w = photoRequest(h, "PUT", path, "image/jpeg", testToken, jpegData); w.Code != 200 {
		t.Fatal("reupload failed")
	}
	request(h, "DELETE", "/api/v1/vehicles/"+v.ID, "")
	ex, err = s.Export(context.Background())
	if err != nil || len(ex.VehiclePhotos) != 0 {
		t.Fatal("vehicle deletion left orphan photo", err)
	}
}

func TestVehiclePhotoAuthenticationAndBounds(t *testing.T) {
	_, h := fixture(t)
	v := vehicle(t, h)
	path := "/api/v1/vehicles/" + v.ID + "/photo"
	valid := testJPEG(t, 8, 8)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if w := photoRequest(h, method, path, "image/jpeg", "wrong", valid); w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", method, w.Code)
		}
	}
	for _, tc := range []struct {
		name, path, contentType string
		body                    []byte
		status                  int
	}{
		{"wrong content type", path, "application/json", valid, 415},
		{"non image", path, "image/jpeg", []byte("not jpeg"), 422},
		{"wide image", path, "image/jpeg", testJPEG(t, 1601, 1), 422},
		{"oversized upload", path, "image/jpeg", make([]byte, garage.MaxVehiclePhotoBytes+1), 413},
		{"missing vehicle", "/api/v1/vehicles/missing/photo", "image/jpeg", valid, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := photoRequest(h, "PUT", tc.path, tc.contentType, testToken, tc.body); w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	if w := request(h, "POST", "/api/v1/vehicles", `{"name":"Bogus","photoRevision":"pretend"}`); w.Code != 422 {
		t.Fatal("client forged photo revision")
	}
	if w := request(h, "PATCH", "/api/v1/vehicles/"+v.ID, `{"photoRevision":"pretend"}`); w.Code != 400 {
		t.Fatal("client patched photo revision")
	}
}
