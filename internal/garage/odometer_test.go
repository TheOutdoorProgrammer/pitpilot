package garage

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func odoFixture(t *testing.T) (*Store, string, time.Time) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "odometer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	v := Vehicle{ID: NewID(), Name: "Odometer fixture", OdometerMiles: 999, OdometerStatus: "estimated", CreatedAt: at}
	if err = s.CreateVehicle(ctx, v); err != nil {
		t.Fatal(err)
	}
	r := Record{ID: "actual", VehicleID: v.ID, Kind: "odometer", Date: at.Format(time.DateOnly), RecordedAt: &at, Title: "Dashboard", OdometerMiles: 1000, OdometerStatus: "measured"}
	if err = s.SaveEntry(ctx, r.ID, v.ID, "record", r, true); err != nil {
		t.Fatal(err)
	}
	return s, v.ID, at
}

func odoDelta(t *testing.T, s *Store, vid, key string, start time.Time, km float64) SignalBatch {
	t.Helper()
	end := start.Add(20 * time.Second)
	b := SignalBatch{Source: "pi", BatchID: key, Observations: []SignalObservation{{Key: key, Metric: "driving_distance_km", Unit: "km", Statistic: "sum", Quality: "estimated", Value: km, PeriodStart: &start, PeriodEnd: &end}}}
	if _, err := s.IngestSignals(context.Background(), vid, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func assertOdo(t *testing.T, s *Store, vid string, miles float64, status string) {
	t.Helper()
	v, err := s.Vehicle(context.Background(), vid)
	if err != nil || math.Abs(v.OdometerMiles-miles) > 1e-9 || v.OdometerStatus != status {
		t.Fatalf("odometer got %.12f %q; want %.12f %q; %v", v.OdometerMiles, v.OdometerStatus, miles, status, err)
	}
}

func TestOdometerBaselineDuplicateOutOfOrderAndCorrection(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	assertOdo(t, s, vid, 1000, "measured")
	b := odoDelta(t, s, vid, "later", at.Add(60*time.Second), 0.3)
	odoDelta(t, s, vid, "earlier", at.Add(20*time.Second), 0.2)
	if _, err := s.IngestSignals(ctx, vid, b); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1000+0.5/1.609344, "estimated")
	correctionAt := at.Add(50 * time.Second)
	r := Record{ID: "correction", VehicleID: vid, Kind: "odometer", Date: correctionAt.Format(time.DateOnly), RecordedAt: &correctionAt, Title: "Corrected actual", OdometerMiles: 1001, OdometerStatus: "measured"}
	if err := s.SaveEntry(ctx, r.ID, vid, "record", r, true); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1001+0.3/1.609344, "estimated")
	odoDelta(t, s, vid, "delayed-before-baseline", at.Add(-30*time.Second), 0.1)
	odoDelta(t, s, vid, "crosses-baseline", at.Add(40*time.Second), 0.1)
	assertOdo(t, s, vid, 1001+0.3/1.609344, "estimated")
	if err := s.DeleteEntry(ctx, r.ID, "record"); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1000+0.6/1.609344, "estimated")
}

func TestOdometerRejectsAmbiguousOverlappingDistance(t *testing.T) {
	s, vid, at := odoFixture(t)
	odoDelta(t, s, vid, "one", at.Add(time.Minute), 0.3)
	assertOdo(t, s, vid, 1000+0.3/1.609344, "estimated")
	odoDelta(t, s, vid, "overlap", at.Add(70*time.Second), 0.3)
	assertOdo(t, s, vid, 1000, "measured")
	v, err := s.Vehicle(context.Background(), vid)
	if err != nil || v.OdometerExcludedIntervals != 2 {
		t.Fatal("missing overlap warning", err)
	}
	odoDelta(t, s, vid, "separate", at.Add(90*time.Second), 0.2)
	assertOdo(t, s, vid, 1000+0.2/1.609344, "estimated")
}

func TestDateOnlyCorrectionUsesAcceptanceCutoffWithoutInventingTime(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	odoDelta(t, s, vid, "before", at.Add(time.Minute), 0.3)
	r := Record{ID: "date-only", VehicleID: vid, Kind: "odometer", Date: at.Format(time.DateOnly), Title: "Actual date only", OdometerMiles: 1002, OdometerStatus: "measured"}
	if err := s.SaveEntry(ctx, r.ID, vid, "record", r, true); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1002, "measured")
	raw, err := s.Entry(ctx, r.ID, "record")
	if err != nil {
		t.Fatal(err)
	}
	var saved Record
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.RecordedAt != nil || saved.CreatedAt != nil {
		t.Fatal("invented capture/creation time")
	}
	if _, err = s.UpdateEntry(ctx, r.ID, "record", func(f map[string]json.RawMessage) error { f["odometerMiles"] = json.RawMessage(`1003`); return nil }); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1003, "measured")
	older := r
	older.ID = "historical"
	older.Date = at.Add(-24 * time.Hour).Format(time.DateOnly)
	older.OdometerMiles = 900
	if err = s.SaveEntry(ctx, older.ID, vid, "record", older, true); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1003, "measured")
}

func TestSmartcarAbsoluteOdometerReplacesBaselineNotAdds(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	for i, when := range []time.Time{at.Add(-time.Minute), at.Add(40 * time.Second)} {
		b := SignalBatch{Source: "smartcar", BatchID: NewID(), Observations: []SignalObservation{{Key: NewID(), Metric: "odometer_km", Unit: "km", Statistic: "sample", Quality: "measured", Value: 1001 * 1.609344, ObservedAt: &when}}}
		if _, err := s.IngestSignals(ctx, vid, b); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			assertOdo(t, s, vid, 1000, "measured")
		}
	}
	odoDelta(t, s, vid, "before-oem", at.Add(10*time.Second), 0.3)
	odoDelta(t, s, vid, "after-oem", at.Add(50*time.Second), 0.2)
	assertOdo(t, s, vid, 1001+0.2/1.609344, "estimated")
}

func TestOdometerBackupPreservesCalibrationAndOriginalRecords(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	odoDelta(t, s, vid, "distance", at.Add(time.Minute), 0.3)
	before, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.OdometerBaselines) != 2 {
		t.Fatal("missing portable baselines")
	}
	var name string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(new(int), new(string), &name); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err = os.WriteFile(restored, data, 0600); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	after, err := copy.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before.OdometerBaselines)
	b, _ := json.Marshal(after.OdometerBaselines)
	if string(a) != string(b) {
		t.Fatal("restart reset calibration")
	}
	assertOdo(t, copy, vid, 1000+0.3/1.609344, "estimated")
}

func TestOdometerOrderingIsTotalAcrossUnknownTimesAndOffsets(t *testing.T) {
	a, _ := time.Parse(time.RFC3339, "2026-10-08T23:30:00-04:00")
	c, _ := time.Parse(time.RFC3339, "2026-10-09T02:00:00Z")
	b, _ := time.Parse(time.RFC3339, "2026-10-09T03:00:00Z")
	baselines := []OdometerBaseline{
		{Key: "newest", CalendarDate: "2026-10-08", RecordedAt: &a, AcceptedAt: a.Add(time.Minute)},
		{Key: "unknown", CalendarDate: "2026-10-08", AcceptedAt: b},
		{Key: "earliest", CalendarDate: "2026-10-09", RecordedAt: &c, AcceptedAt: c.Add(2 * time.Hour)},
	}
	for _, indices := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		values := []OdometerBaseline{baselines[indices[0]], baselines[indices[1]], baselines[indices[2]]}
		sort.Slice(values, func(i, j int) bool { return odometerBaselineLater(values[i], values[j]) })
		if values[0].Key != "newest" || values[1].Key != "earliest" || values[2].Key != "unknown" {
			t.Fatal("order depends on input or timezone", indices)
		}
	}
	older := OdometerBaseline{Key: "old-date", CalendarDate: "2020-01-01", AcceptedAt: time.Now()}
	if odometerBaselineLater(older, baselines[2]) {
		t.Fatal("backdated record promoted to current")
	}
}

func TestExplicitVehicleCorrectionCanReturnToStoredRawMileage(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	odoDelta(t, s, vid, "distance", at.Add(time.Minute), 0.2)
	if _, err := s.UpdateVehicle(ctx, vid, false, func(v *Vehicle) error { v.Name = "Renamed"; return nil }); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1000+0.2/1.609344, "estimated")
	if _, err := s.UpdateVehicle(ctx, vid, true, func(v *Vehicle) error { v.OdometerMiles = 999; v.OdometerStatus = "measured"; return nil }); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 999, "measured")
}

func TestVehicleReadingMethodOnlyCorrectionUsesProjectedMileage(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	odoDelta(t, s, vid, "distance", at.Add(time.Minute), 0.2)
	if _, err := s.UpdateVehicle(ctx, vid, true, func(v *Vehicle) error { v.OdometerStatus = "measured"; return nil }); err != nil {
		t.Fatal(err)
	}
	assertOdo(t, s, vid, 1000+0.2/1.609344, "measured")
}

func TestDistanceWithoutKnownBaselineDoesNotInventAnOdometer(t *testing.T) {
	s, vid, at := odoFixture(t)
	ctx := context.Background()
	if err := s.DeleteEntry(ctx, "actual", "record"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE vehicles SET data=json_set(data,'$.odometerMiles',0,'$.odometerStatus','unknown') WHERE id=?", vid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE odometer_baselines SET data=json_set(data,'$.miles',0,'$.status','unknown','$.acceptedAt',?) WHERE vehicle_id=?", at.Format(time.RFC3339Nano), vid); err != nil {
		t.Fatal(err)
	}
	odoDelta(t, s, vid, "distance", at.Add(time.Minute), 0.2)
	assertOdo(t, s, vid, 0, "unknown")
}

func TestDistanceContractRejectsMutableOrUnboundedIncrements(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	end := at.Add(20 * time.Second)
	o := SignalObservation{Key: "interval", Metric: "driving_distance_km", Unit: "km", Statistic: "sum", Quality: "estimated", Value: 0.3, PeriodStart: &at, PeriodEnd: &end}
	for _, mutate := range []func(*SignalObservation){
		func(o *SignalObservation) { o.SourceRevision = &at }, func(o *SignalObservation) { o.Quality = "measured" },
		func(o *SignalObservation) { later := at.Add(time.Minute); o.PeriodEnd = &later }, func(o *SignalObservation) { o.Value = 100 },
	} {
		candidate := o
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatal("accepted unsafe distance")
		}
	}
}
