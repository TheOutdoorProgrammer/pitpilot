package garage

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestDevicePowerPolicyJSONRequiresExplicitCompleteZone(t *testing.T) {
	initial := DevicePowerPolicy{DeviceID: "synthetic-device", VehicleID: "synthetic-vehicle", PowerSafeZone: &PowerSafeZone{Latitude: 0, Longitude: 0, RadiusMeters: 152.4}}
	for _, raw := range []string{
		`null`, `{}`, `{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle"}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":null,"extra":true}`,
		`{"deviceId":"","vehicleId":"synthetic-vehicle","powerSafeZone":null}`,
		`{"deviceId":"synthetic-device","powerSafeZone":null}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{"longitude":0,"radiusMeters":152.4}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{"latitude":0,"radiusMeters":152.4}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{"latitude":0,"longitude":0}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{"latitude":null,"longitude":0,"radiusMeters":152.4}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{"latitude":"0","longitude":0,"radiusMeters":152.4}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":{"latitude":0,"longitude":0,"radiusMeters":152.4,"extra":true}}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":false}`,
		`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":null} {}`,
	} {
		got := initial
		if json.Unmarshal([]byte(raw), &got) == nil {
			t.Fatal("incomplete or unknown policy fields accepted")
		}
		if !reflect.DeepEqual(got, initial) {
			t.Fatal("rejected policy cleared or changed the prior zone")
		}
	}
	raw, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip DevicePowerPolicy
	if err := json.Unmarshal(raw, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, initial) {
		t.Fatal("valid zero-coordinate zone did not round trip", err)
	}
	if err := json.Unmarshal([]byte(`{"deviceId":"synthetic-device","vehicleId":"synthetic-vehicle","powerSafeZone":null}`), &roundTrip); err != nil || roundTrip.PowerSafeZone != nil {
		t.Fatal("explicit null did not clear the policy", err)
	}
}

func TestPowerSafeZoneValidation(t *testing.T) {
	for _, z := range []PowerSafeZone{{0, 0, 152.4}, {-90, -180, 25}, {90, 180, 5000}} {
		if err := z.Validate(); err != nil {
			t.Fatal("valid boundary rejected", err)
		}
	}
	for _, z := range []PowerSafeZone{
		{math.NaN(), 0, 152.4}, {math.Inf(1), 0, 152.4}, {0, math.NaN(), 152.4}, {0, math.Inf(-1), 152.4},
		{0, 0, math.NaN()}, {0, 0, math.Inf(1)}, {-90.01, 0, 152.4}, {0, 180.01, 152.4}, {0, 0, 24.99}, {0, 0, 5000.01},
	} {
		if z.Validate() == nil {
			t.Fatal("invalid zone accepted")
		}
	}
}

func TestDevicePowerPolicySurvivesHeartbeatAndValidatesBeforeMutation(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	e, err := s.CreateDevice(ctx, id, "Power policy Pi")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(ctx, e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	zone := &PowerSafeZone{Latitude: 0, Longitude: 0, RadiusMeters: 152.4}
	if _, err := s.SetDevicePolicy(ctx, d.DeviceID, nil, nil, &zone); err != nil {
		t.Fatal(err)
	}
	heartbeat := DeviceHeartbeat{Version: "0.5.9", CollectionState: "collecting", UpdateState: "healthy", GPSState: "fix"}
	if err := s.HeartbeatDevice(ctx, d.Token, heartbeat); err != nil {
		t.Fatal(err)
	}
	got, err := s.DevicePowerPolicy(ctx, d.Token)
	if err != nil || !reflect.DeepEqual(got.PowerSafeZone, zone) {
		t.Fatal("heartbeat discarded zone", err)
	}
	bad := &PowerSafeZone{Latitude: math.NaN(), Longitude: 0, RadiusMeters: 152.4}
	changed := false
	if _, err := s.SetDevicePolicy(ctx, d.DeviceID, &changed, nil, &bad); err == nil {
		t.Fatal("store accepted nonfinite zone")
	}
	got, err = s.DevicePowerPolicy(ctx, d.Token)
	if err != nil || !reflect.DeepEqual(got.PowerSafeZone, zone) {
		t.Fatal("failed validation changed persisted zone", err)
	}
	config, err := s.DeviceConfiguration(ctx, d.Token)
	if err != nil || !config.AutoUpdate {
		t.Fatal("invalid zone partially changed update policy", err)
	}
}
