package garage

import "testing"

func TestEveryPublicSignalHasAnExplanation(t *testing.T) {
	for _, d := range SignalDefinitions() {
		if d.Description == "" || d.Interpretation == "" {
			t.Errorf("%s needs useful description and interpretation", d.Metric)
		}
	}
}
