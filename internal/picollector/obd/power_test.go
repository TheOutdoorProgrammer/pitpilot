package obdcollector

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestSleepingECUKeepsFreshVoltageWithoutFabricatingSample(t *testing.T) {
	elm, port := newScriptedELM(t)
	d, err := openStream(testContext(t), port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	elm.set("010C", "NO DATA")
	elm.set("ATRV", "12.1V")
	first, err := d.SampleDetails(testContext(t))
	if !errors.Is(err, ErrECUUnavailable) || !reflect.DeepEqual(first.Readings, map[string]float64{"adapter_voltage_v": 12.1}) || first.DTCs != nil || first.Power.RPM != nil || !first.Power.ECUNoData || first.Power.At.IsZero() {
		t.Fatalf("engine no-data classification: %+v, %v", first, err)
	}
	elm.set("ATRV", "11.9V")
	second, err := d.SampleDetails(testContext(t))
	if !errors.Is(err, ErrECUUnavailable) || *second.Power.Voltage != 11.9 || !second.Power.At.After(first.Power.At) {
		t.Fatalf("voltage was stale: %+v, %v", second, err)
	}
	elm.set("010C", "486B10410C0BB8AA")
	third, err := d.SampleDetails(testContext(t))
	if err != nil || third.Readings["rpm"] != 750 || third.Power.ECUNoData {
		t.Fatalf("engine did not resume on same session: %+v, %v", third, err)
	}
	if count := len(slices.DeleteFunc(elm.commands(), func(s string) bool { return s != "ATZ" })); count != 1 {
		t.Fatalf("sleep caused %d adapter resets", count)
	}
}

func TestSleepingECUWithMalformedVoltageHasNoObservation(t *testing.T) {
	elm, port := newScriptedELM(t)
	d, err := openStream(testContext(t), port)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	elm.set("010C", "NO DATA")
	elm.set("ATRV", "?")
	sample, err := d.SampleDetails(testContext(t))
	if !errors.Is(err, ErrECUUnavailable) || len(sample.Readings) != 0 || sample.DTCs != nil || sample.PendingDTCs != nil || sample.PermanentDTCs != nil {
		t.Fatal("empty unavailable sample fixture changed", sample, err)
	}
}

func TestPowerClassificationRejectsAmbiguousMissingAndMalformedRPM(t *testing.T) {
	for _, reply := range []string{"", "486B18410C0000AA", "486B10410500AA", "486B10410C00AA", "486B107F0131AA", "BUS ERROR", "!UNABLE TO CONNECT", "123<DATA ERROR"} {
		t.Run(reply, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			d, err := openStream(testContext(t), port)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			elm.set("010C", reply)
			o, err := d.SamplePower(testContext(t))
			if err == nil || o.ECUNoData || o.RPM != nil || o.Voltage != nil {
				t.Fatalf("unknown reply became off evidence: %+v, %v", o, err)
			}
		})
	}
}

func TestInvalidVoltageIsUnknownAndCanceledReadIsRejected(t *testing.T) {
	for _, reply := range []string{"NaNV", "InfinityV", "12.1V\r12.2V", "BUS ERROR", "?"} {
		t.Run(reply, func(t *testing.T) {
			elm, port := newScriptedELM(t)
			d, err := openStream(testContext(t), port)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			elm.set("ATRV", reply)
			o, err := d.SamplePower(testContext(t))
			if err != nil || o.Voltage != nil {
				t.Fatalf("bad voltage accepted: %+v, %v", o, err)
			}
		})
	}
	_, port := newScriptedELM(t)
	d, err := openStream(testContext(t), port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.SamplePower(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	if err := port.Close(); err != nil {
		t.Fatal(err)
	}
	if o, err := d.SamplePower(testContext(t)); err == nil || o.Voltage != nil || o.RPM != nil || o.ECUNoData {
		t.Fatalf("closed adapter became engine-off evidence: %+v, %v", o, err)
	}
}
