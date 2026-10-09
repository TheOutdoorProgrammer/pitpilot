package picollector

import (
	"math"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
)

type speedSample struct {
	at  time.Time
	kph float64
}

func speedObservation(sample obd.Observation, at time.Time) *speedSample {
	speed, ok := sample.Readings["speed_kph"]
	if !ok || math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 || speed > 400 {
		return nil
	}
	return &speedSample{at: at, kph: speed}
}

// Only adjacent, durably queued samples in this process can form an interval.
// Monotonic time detects wall-clock steps and prevents restart/gap extrapolation.
func drivingDistance(previous, current *speedSample, key string) *garage.SignalObservation {
	if previous == nil || current == nil {
		return nil
	}
	elapsed := current.at.Sub(previous.at)
	wall := current.at.UTC().Sub(previous.at.UTC())
	if elapsed <= 0 || elapsed > 30*time.Second || wall <= 0 || math.Abs(float64(wall-elapsed)) > float64(2*time.Second) {
		return nil
	}
	start, end := previous.at.UTC(), current.at.UTC()
	return &garage.SignalObservation{Key: key + ":distance", Metric: "driving_distance_km", Unit: "km", Statistic: "sum", Quality: "estimated", Value: (previous.kph + current.kph) / 2 * elapsed.Hours(), PeriodStart: &start, PeriodEnd: &end}
}
