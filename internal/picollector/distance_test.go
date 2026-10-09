package picollector

import (
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
	"testing"
	"time"
)

func TestDrivingDistanceUsesAdjacentSamplesAndKeepsFractions(t *testing.T) {
	at := time.Now()
	a := speedObservation(obd.Observation{Readings: map[string]float64{"speed_kph": 36}}, at)
	b := speedObservation(obd.Observation{Readings: map[string]float64{"speed_kph": 72}}, at.Add(10*time.Second))
	d := drivingDistance(a, b, "stable")
	if d == nil || d.Value != 0.15 || d.Quality != "estimated" || d.Statistic != "sum" || d.ObservedAt != nil {
		t.Fatalf("bad interval: %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if again := drivingDistance(a, b, "stable"); again.Key != d.Key {
		t.Fatal("unstable key")
	}
	for _, tc := range []struct {
		name string
		a, b *speedSample
	}{
		{"restart", nil, b}, {"missing speed", a, speedObservation(obd.Observation{}, at)},
		{"gap", a, &speedSample{at: at.Add(31 * time.Second), kph: 72}},
		{"reverse clock", b, a}, {"same time", a, a},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if drivingDistance(tc.a, tc.b, "test") != nil {
				t.Fatal("invented distance")
			}
		})
	}
}
