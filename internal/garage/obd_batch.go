package garage

import (
	"errors"
	"sort"
	"time"
)

// MeasuredOBDBatch gives current collectors and receiver recovery the same units,
// sample semantics and explicit unknown diagnostic states.
func MeasuredOBDBatch(readings map[string]float64, stored, pending, permanent []string, at time.Time, id string) (SignalBatch, error) {
	b := SignalBatch{Source: "pi", BatchID: id}
	if len(readings) == 0 && stored == nil && pending == nil && permanent == nil {
		return b, errors.New("empty adapter observation")
	}
	units := map[string]string{}
	for _, d := range SignalDefinitions() {
		units[d.Metric] = d.Unit
	}
	keys := make([]string, 0, len(readings))
	for key := range readings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.Observations = append(b.Observations, SignalObservation{Key: id + ":" + key, Metric: key, Unit: units[key], Statistic: "sample", Quality: "measured", Value: readings[key], ObservedAt: &at})
	}
	for _, d := range []struct {
		class string
		codes []string
	}{{"stored", stored}, {"pending", pending}, {"permanent", permanent}} {
		reads := 1
		if d.codes == nil {
			reads = 0
		}
		b.Contexts = append(b.Contexts, SignalContext{Key: id + ":" + d.class, Kind: "diagnostic", ObservedAt: &at, Diagnostic: &SignalDiagnostic{Class: d.class, SuccessfulReads: reads, Unknown: d.codes == nil, Codes: d.codes}})
	}
	return b, b.Validate()
}
