package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func TestDevicePowerPolicyIsScopedAndSparsePatchesPreserveIt(t *testing.T) {
	s, h := fixture(t)
	ctx := context.Background()
	v := vehicle(t, h)
	e, err := s.CreateDevice(ctx, v.ID, "Synthetic zone Pi")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(ctx, e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	otherVehicle := vehicle(t, h)
	otherEnrollment, err := s.CreateDevice(ctx, otherVehicle.ID, "Other zone Pi")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.EnrollDevice(ctx, otherEnrollment.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "/api/v1/device/power-policy"
	patch := "/api/v1/devices/" + d.DeviceID
	zoneJSON := `{"powerSafeZone":{"latitude":0,"longitude":0,"radiusMeters":152.4}}`
	for _, token := range []string{"", testToken, e.EnrollmentToken, strings.Repeat("x", 43)} {
		w := deviceAPIRequest(h, "GET", endpoint, token, "")
		if w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("power policy accepted non-device authorization", w.Code)
		}
	}
	if w := deviceAPIRequest(h, "PATCH", patch, d.Token, zoneJSON); w.Code != 401 {
		t.Fatal("device token changed household policy", w.Code)
	}
	read := func(token string) garage.DevicePowerPolicy {
		t.Helper()
		w := deviceAPIRequest(h, "GET", endpoint, token, "")
		var got garage.DevicePowerPolicy
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("power policy read failed: status %d", w.Code)
		}
		if got.PowerSafeZone == nil && !strings.Contains(w.Body.String(), `"powerSafeZone":null`) {
			t.Fatal("unset power policy was omitted instead of explicit null")
		}
		return got
	}
	if initial := read(d.Token); initial.DeviceID != d.DeviceID || initial.VehicleID != v.ID || initial.PowerSafeZone != nil {
		t.Fatal("new device has incorrect policy identity or invented zone")
	}
	if w := request(h, "PATCH", patch, zoneJSON); w.Code != 200 {
		t.Fatal("zone patch rejected", w.Code)
	}
	want := &garage.PowerSafeZone{Latitude: 0, Longitude: 0, RadiusMeters: 152.4}
	if got := read(d.Token); !reflect.DeepEqual(got.PowerSafeZone, want) {
		t.Fatal("zone not persisted")
	}
	if got := read(other.Token); got.DeviceID != other.DeviceID || got.VehicleID != otherVehicle.ID || got.PowerSafeZone != nil {
		t.Fatal("policy leaked across device bindings")
	}
	if w := request(h, "PATCH", patch, `{"autoUpdate":false,"gpsRecording":true}`); w.Code != 200 {
		t.Fatal("sparse policy patch rejected", w.Code)
	}
	if got := read(d.Token); !reflect.DeepEqual(got.PowerSafeZone, want) {
		t.Fatal("unrelated patch cleared zone")
	}
	if w := request(h, "PATCH", patch, `{"powerSafeZone":null}`); w.Code != 200 {
		t.Fatal("explicit zone removal rejected", w.Code)
	}
	if got := read(d.Token); got.PowerSafeZone != nil {
		t.Fatal("explicit null did not clear zone")
	}
	config, err := s.DeviceConfiguration(ctx, d.Token)
	if err != nil || config.AutoUpdate || !config.GPSRecording {
		t.Fatal("zone patch modified unrelated policy", err)
	}
	if w := request(h, "DELETE", patch, ""); w.Code != 204 {
		t.Fatal("revoke failed", w.Code)
	}
	if w := deviceAPIRequest(h, "GET", endpoint, d.Token, ""); w.Code != 401 {
		t.Fatal("revoked device retrieved policy", w.Code)
	}
	if w := request(h, "PATCH", patch, zoneJSON); w.Code != 404 {
		t.Fatal("revoked device policy changed", w.Code)
	}
}

func TestDevicePowerPolicyRejectsMalformedZonesWithoutChangingPolicy(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	e, err := s.CreateDevice(context.Background(), v.ID, "Validation Pi")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(context.Background(), e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	patch := "/api/v1/devices/" + d.DeviceID
	if w := request(h, "PATCH", patch, `{"powerSafeZone":{"latitude":0,"longitude":0,"radiusMeters":152.4}}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	before, err := s.Devices(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		zone string
		code int
	}{
		{`{}`, 422},
		{`{"longitude":0,"radiusMeters":152.4}`, 422},
		{`{"latitude":0,"radiusMeters":152.4}`, 422},
		{`{"latitude":0,"longitude":0}`, 422},
		{`{"latitude":null,"longitude":0,"radiusMeters":152.4}`, 422},
		{`{"latitude":91,"longitude":0,"radiusMeters":152.4}`, 422},
		{`{"latitude":0,"longitude":-181,"radiusMeters":152.4}`, 422},
		{`{"latitude":0,"longitude":0,"radiusMeters":24.99}`, 422},
		{`{"latitude":0,"longitude":0,"radiusMeters":5000.01}`, 422},
		{`{"latitude":0,"longitude":0,"radiusMeters":1e309}`, 400},
		{`{"latitude":"0","longitude":0,"radiusMeters":152.4}`, 400},
		{`{"latitude":0,"longitude":0,"radiusMeters":152.4,"extra":true}`, 400},
		{`[]`, 400},
		{`true`, 400},
	} {
		w := request(h, "PATCH", patch, `{"autoUpdate":false,"gpsRecording":true,"powerSafeZone":`+tc.zone+`}`)
		if w.Code != tc.code {
			t.Fatalf("invalid zone status %d, want %d", w.Code, tc.code)
		}
	}
	after, err := s.Devices(context.Background(), v.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected patch changed device policy", err)
	}
}

func TestPowerPolicyDoesNotChangeStrictConfigOrEnrollmentShapes(t *testing.T) {
	s, h := fixture(t)
	v := vehicle(t, h)
	e, err := s.CreateDevice(context.Background(), v.ID, "Compatibility Pi")
	if err != nil {
		t.Fatal(err)
	}
	patch := "/api/v1/devices/" + e.Device.ID
	if w := request(h, "PATCH", patch, `{"gpsRecording":true,"powerSafeZone":{"latitude":0,"longitude":0,"radiusMeters":152.4}}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	raw, _ := json.Marshal(map[string]string{"enrollmentToken": e.EnrollmentToken})
	w := deviceAPIRequest(h, "POST", "/api/v1/device/enroll", "", string(raw))
	var enrolled garage.EnrolledDevice
	decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
	decoder.DisallowUnknownFields()
	if w.Code != 200 || decoder.Decode(&enrolled) != nil {
		t.Fatal("new policy broke strict legacy enrollment shape")
	}
	for _, capability := range []string{"", "gps-v1", "unknown"} {
		req := httptest.NewRequest("GET", "/api/v1/device/config", nil)
		req.Header.Set("Authorization", "Bearer "+enrolled.Token)
		req.Header.Set("X-PitPilot-Capabilities", capability)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var fields map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &fields) != nil {
			t.Fatal("existing config unavailable")
		}
		wantCount := 4
		if capability == "gps-v1" {
			wantCount++
			if string(fields["gpsRecording"]) != "true" {
				t.Fatal("GPS capability was lost")
			}
		}
		if len(fields) != wantCount || fields["powerSafeZone"] != nil {
			t.Fatal("power policy leaked into strict config contract")
		}
	}
}
