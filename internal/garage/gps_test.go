package garage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func gpsFix(key, recording string, at time.Time, latitude, longitude, speed float64) SignalContext {
	hdop := 0.9
	satellites := 8
	quality := 1
	return SignalContext{Key: key, Kind: "location", ObservedAt: &at, Location: &SignalLocation{Type: "gps", RecordingID: recording, Latitude: latitude, Longitude: longitude, SpeedKPH: &speed, HDOP: &hdop, Satellites: &satellites, FixQuality: &quality}}
}
func gpsIngest(t *testing.T, s *Store, id string, fixes ...SignalContext) SignalBatch {
	t.Helper()
	batch := SignalBatch{Source: "pi", BatchID: NewID(), Contexts: fixes}
	if _, err := s.IngestSignals(context.Background(), id, batch); err != nil {
		t.Fatal(err)
	}
	return batch
}
func gpsTrips(t *testing.T, s *Store, id string) []Trip {
	t.Helper()
	out, err := s.Trips(context.Background(), id, 100, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGPSAutomaticTripsOutOfOrderDuplicateAndSeparateOdometer(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	fixes := []SignalContext{gpsFix("one", "session", at, 40, -75, 36), gpsFix("two", "session", at.Add(10*time.Second), 40.001, -75, 36), gpsFix("three", "session", at.Add(20*time.Second), 40.002, -75, 36)}
	before, err := s.Vehicle(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	late := gpsIngest(t, s, id, fixes[2])
	gpsIngest(t, s, id, fixes[0], fixes[1])
	trips := gpsTrips(t, s, id)
	if len(trips) != 1 || len(trips[0].Points) != 3 || trips[0].Source != "pi-gps" || trips[0].DistanceQuality != "derived" || math.Abs(trips[0].DistanceMiles-0.13819) > 0.001 {
		t.Fatal("incorrect derived trip")
	}
	if r, err := s.IngestSignals(ctx, id, late); err != nil || r.ContextsSkipped != 1 {
		t.Fatal("retry changed fix", err)
	}
	if !reflect.DeepEqual(trips, gpsTrips(t, s, id)) {
		t.Fatal("retry changed projection")
	}
	location, err := s.LatestLocation(ctx, id)
	if err != nil || location == nil || !location.RecordedAt.Equal(*fixes[2].ObservedAt) || location.AccuracyMeters != nil || location.HDOP == nil {
		t.Fatal("incorrect last known location", err)
	}
	after, err := s.Vehicle(ctx, id)
	if err != nil || after.OdometerMiles != before.OdometerMiles || after.OdometerStatus != before.OdometerStatus {
		t.Fatal("GPS changed odometer", err)
	}
	ordered := automaticTrips(id, "session", append([]SignalContext(nil), fixes...))
	if !reflect.DeepEqual(ordered, trips) {
		t.Fatal("arrival order affected trips")
	}
}

func TestGPSGapsJumpsStopsAndNoFixes(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if got := automaticTrips("v", "s", nil); len(got) != 0 {
		t.Fatal("invented empty trip")
	}
	for name, fixes := range map[string][]SignalContext{
		"gap":  {gpsFix("a", "s", at, 40, -75, 40), gpsFix("b", "s", at.Add(10*time.Second), 40.001, -75, 40), gpsFix("c", "s", at.Add(5*time.Minute), 41, -75, 40), gpsFix("d", "s", at.Add(5*time.Minute+10*time.Second), 41.001, -75, 40)},
		"jump": {gpsFix("a", "s", at, 40, -75, 40), gpsFix("b", "s", at.Add(10*time.Second), 40.001, -75, 40), gpsFix("c", "s", at.Add(20*time.Second), 41, -75, 40), gpsFix("d", "s", at.Add(30*time.Second), 41.001, -75, 40)},
	} {
		t.Run(name, func(t *testing.T) {
			trips := automaticTrips("v", "s", fixes)
			if len(trips) != 2 {
				t.Fatal("did not split unsafe route", len(trips))
			}
			for _, trip := range trips {
				if trip.DistanceMiles > 1 {
					t.Fatal("invented distance across discontinuity")
				}
			}
		})
	}
	fixes := []SignalContext{gpsFix("a", "s", at, 40, -75, 40), gpsFix("b", "s", at.Add(10*time.Second), 40.001, -75, 40)}
	for i := 0; i <= 6; i++ {
		fixes = append(fixes, gpsFix(fmt.Sprintf("stop%d", i), "s", at.Add(time.Duration(20+60*i)*time.Second), 40.001, -75, 0))
	}
	fixes = append(fixes, gpsFix("restart1", "s", at.Add(390*time.Second), 40.002, -75, 40), gpsFix("restart2", "s", at.Add(400*time.Second), 40.003, -75, 40))
	if trips := automaticTrips("v", "s", fixes); len(trips) != 2 || trips[0].EndedAt.After(at.Add(20*time.Second)) {
		t.Fatal("long stop did not end trip")
	}
	stationary := []SignalContext{gpsFix("a", "s", at, 40, -75, 0), gpsFix("b", "s", at.Add(10*time.Second), 40.00002, -75, 0)}
	if len(automaticTrips("v", "s", stationary)) != 0 {
		t.Fatal("stationary drift became trip")
	}
}

func TestGPSDeletionBlocksReplayAndLateBridge(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	batch := gpsIngest(t, s, id, gpsFix("a", "s", at, 40, -75, 40), gpsFix("b", "s", at.Add(10*time.Second), 40.001, -75, 40))
	trip := gpsTrips(t, s, id)[0]
	if err := s.DeleteEntry(ctx, trip.ID, "trip"); err != nil {
		t.Fatal(err)
	}
	if len(gpsTrips(t, s, id)) != 0 {
		t.Fatal("trip not deleted")
	}
	if report, err := s.IngestSignals(ctx, id, batch); err != nil || report.ContextsSkipped != 2 {
		t.Fatal("deleted samples not acknowledged safely", report, err)
	}
	gpsIngest(t, s, id, gpsFix("late before", "s", at.Add(-10*time.Second), 39.999, -75, 40), gpsFix("late after", "s", at.Add(20*time.Second), 40.002, -75, 40))
	if len(gpsTrips(t, s, id)) != 0 {
		t.Fatal("late bridge recreated deleted trip")
	}
	if err := s.ClearGPSHistory(ctx, id); err != nil {
		t.Fatal(err)
	}
	if location, err := s.LatestLocation(ctx, id); err != nil || location != nil {
		t.Fatal("history clear retained location", err)
	}
	if _, err := s.IngestSignals(ctx, id, batch); err != nil {
		t.Fatal(err)
	}
	if len(gpsTrips(t, s, id)) != 0 {
		t.Fatal("history replay resurrected trip")
	}
	export, err := s.Export(ctx)
	if err != nil || len(export.GPSHistoryPolicies) != 1 {
		t.Fatal("missing deletion policy in export", err)
	}
}

func TestGPSDeviceScopeAndRevocation(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	batch := SignalBatch{Source: "pi", BatchID: "same", Contexts: []SignalContext{gpsFix("one", "same-session", at, 40, -75, 40), gpsFix("two", "same-session", at.Add(10*time.Second), 40.001, -75, 40)}}
	var tokens []string
	var deviceIDs []string
	for range 2 {
		e, err := s.CreateDevice(ctx, id, "GPS fixture")
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.EnrollDevice(ctx, e.EnrollmentToken)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, d.Token)
		deviceIDs = append(deviceIDs, d.DeviceID)
		if _, err = s.IngestDeviceSignals(ctx, d.Token, batch); err != nil {
			t.Fatal(err)
		}
	}
	if len(gpsTrips(t, s, id)) != 2 {
		t.Fatal("device recording identities collided")
	}
	if err := s.RevokeDevice(ctx, deviceIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestDeviceSignals(ctx, tokens[0], batch); err != ErrDeviceUnauthorized {
		t.Fatal("revoked GPS accepted", err)
	}
	if len(gpsTrips(t, s, id)) != 2 {
		t.Fatal("revocation erased recorded history")
	}
}

func TestGPSProjectionAndTombstonesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gps.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := NewID()
	s.CreateVehicle(ctx, Vehicle{ID: id, Name: "GPS fixture"})
	at := time.Now().UTC().Add(-time.Hour)
	batch := gpsIngest(t, s, id, gpsFix("one", "s", at, 0, 0, 40), gpsFix("two", "s", at.Add(time.Minute), 0.001, 0, 40))
	before, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before.AutomaticTrips)
	b, _ := json.Marshal(after.AutomaticTrips)
	if string(a) != string(b) {
		t.Fatal("reopen changed projection")
	}
	if location, err := s.LatestLocation(ctx, id); err != nil || location == nil || location.Longitude != 0 {
		t.Fatal("lost real zero coordinate", err)
	}
	if err := s.ClearGPSHistory(ctx, id); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if report, err := s.IngestSignals(ctx, id, batch); err != nil || report.ContextsSkipped != 2 {
		t.Fatal("reopen lost replay protection", err)
	}
	if location, err := s.LatestLocation(ctx, id); err != nil || location != nil || len(gpsTrips(t, s, id)) != 0 {
		t.Fatal("reopen resurrected deleted history", err)
	}
}

func TestTripDisplayReductionKeepsGapEndpointsAndRawExport(t *testing.T) {
	s, id := signalFixture(t)
	at := time.Now().UTC().Add(-10 * time.Hour)
	points := make([]Point, 2001)
	for i := range points {
		when := at.Add(time.Duration(i) * time.Second)
		if i >= 901 {
			when = when.Add(10 * time.Minute)
		}
		points[i] = Point{Latitude: float64(i) / 100000, Longitude: 0, RecordedAt: when}
	}
	trip := Trip{ID: "long-manual", VehicleID: id, Title: "Long fixture", StartedAt: points[0].RecordedAt, EndedAt: points[len(points)-1].RecordedAt, Points: points}
	if err := s.SaveEntry(context.Background(), trip.ID, id, "trip", trip, true); err != nil {
		t.Fatal(err)
	}
	listed := gpsTrips(t, s, id)[0]
	if !listed.RouteSimplified || listed.RecordedPointCount != len(points) || len(listed.Points) > 1000 || listed.Points[0] != points[0] || listed.Points[len(listed.Points)-1] != points[len(points)-1] {
		t.Fatal("display reduction lost bounds or provenance")
	}
	gapEndpoints := 0
	for _, p := range listed.Points {
		if p == points[900] || p == points[901] {
			gapEndpoints++
		}
	}
	if gapEndpoints != 2 {
		t.Fatal("display reduction hid a recording gap")
	}
	export, err := s.Export(context.Background())
	if err != nil || len(export.Trips) != 1 {
		t.Fatal("display reduction mutated full export", err)
	}
	var original Trip
	if err := json.Unmarshal(export.Trips[0], &original); err != nil || len(original.Points) != len(points) {
		t.Fatal("display reduction mutated original points", err)
	}
}

func TestTripPaginationPreservesStableEqualTimeCursor(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	for _, key := range []string{"a", "b", "c"} {
		trip := Trip{ID: key, VehicleID: id, Title: "Manual", StartedAt: at, EndedAt: at, Points: []Point{}}
		if err := s.SaveEntry(ctx, key, id, "trip", trip, true); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Trips(ctx, id, 2, time.Time{}, "")
	if err != nil || len(first) != 2 || first[0].ID != "c" || first[1].ID != "b" {
		t.Fatal("bad first page", err)
	}
	second, err := s.Trips(ctx, id, 2, first[1].StartedAt, first[1].ID)
	if err != nil || len(second) != 1 || second[0].ID != "a" {
		t.Fatal("cursor lost equal timestamp row", err)
	}
}

func TestLegacyGPSContextsRemainValidWithoutNativeProjection(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	legacy := SignalContext{Key: "legacy-gps", Kind: "location", ObservedAt: &at, Location: &SignalLocation{Type: "gps", Latitude: 0, Longitude: 0}}
	if err := legacy.Validate(); err != nil {
		t.Fatal("legacy GPS contract rejected", err)
	}
	batch := SignalBatch{Source: "pi", BatchID: "legacy-batch", Contexts: []SignalContext{legacy}}
	e, err := s.CreateDevice(ctx, id, "Legacy fixture")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnrollDevice(ctx, e.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestDeviceSignals(ctx, d.Token, batch); err != nil {
		t.Fatal("legacy device context rejected", err)
	}
	if report, err := s.IngestDeviceSignals(ctx, d.Token, batch); err != nil || report.ContextsSkipped != 1 {
		t.Fatal("legacy device retry changed identity", err)
	}
	dateOnly := legacy
	dateOnly.Key = "legacy-date"
	dateOnly.ObservedAt = nil
	dateOnly.CalendarDate = "2026-10-01"
	dateOnly.Timezone = "unknown"
	gpsIngest(t, s, id, dateOnly)
	if len(gpsTrips(t, s, id)) != 0 {
		t.Fatal("legacy points acquired native trips")
	}
	if location, err := s.LatestLocation(ctx, id); err != nil || location != nil {
		t.Fatal("legacy context relabeled as verified native fix", err)
	}
	if err := s.ClearGPSHistory(ctx, id); err != nil {
		t.Fatal(err)
	}
	export, err := s.Export(ctx)
	if err != nil || len(export.SignalContexts) != 2 {
		t.Fatal("native history deletion erased legacy contexts", err)
	}
	for _, c := range export.SignalContexts {
		if c.Context.Location.RecordingID != "" {
			t.Fatal("device assigned native recording identity to legacy context")
		}
	}
	partial := legacy
	location := *legacy.Location
	location.RecordingID = "incomplete-native"
	partial.Location = &location
	if partial.Validate() == nil {
		t.Fatal("partial native fix accepted")
	}
}

func TestLatestLocationUsesActualSmartcarTimestampAcrossSources(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	pi := gpsFix("pi", "recording", at, 0, 0, 0)
	gpsIngest(t, s, id, pi)
	newer := at.Add(900 * time.Nanosecond)
	accuracy := 12.0
	smartcar := SignalContext{Key: "smartcar", Kind: "location", ObservedAt: &newer, Location: &SignalLocation{Type: "LAST_PARKED", Latitude: 0, Longitude: 0, AccuracyMeters: &accuracy}}
	batch := SignalBatch{Source: "smartcar", BatchID: "smartcar-actual", Contexts: []SignalContext{smartcar}}
	if _, err := s.IngestSignals(ctx, id, batch); err != nil {
		t.Fatal(err)
	}
	calendar := smartcar
	calendar.Key = "smartcar-calendar"
	calendar.ObservedAt = nil
	calendar.CalendarDate = time.Now().UTC().Format(time.DateOnly)
	calendar.Timezone = "unknown"
	if _, err := s.IngestSignals(ctx, id, SignalBatch{Source: "smartcar", BatchID: "calendar", Contexts: []SignalContext{calendar}}); err != nil {
		t.Fatal(err)
	}
	location, err := s.LatestLocation(ctx, id)
	if err != nil || location == nil || location.Source != "smartcar" || !location.RecordedAt.Equal(newer) || location.Latitude != 0 || location.Longitude != 0 || location.AccuracyMeters == nil || *location.AccuracyMeters != accuracy {
		t.Fatal("latest actual Smartcar position or provenance lost", err)
	}
	if len(gpsTrips(t, s, id)) != 0 {
		t.Fatal("sparse Smartcar location created a trip")
	}
	if err := s.ClearGPSHistory(ctx, id); err != nil {
		t.Fatal(err)
	}
	if location, err = s.LatestLocation(ctx, id); err != nil || location == nil || location.Source != "smartcar" || !location.RecordedAt.Equal(newer) {
		t.Fatal("native GPS deletion erased Smartcar position", err)
	}
}

func TestCalendarOnlySmartcarLocationNeverInventsCaptureTime(t *testing.T) {
	s, id := signalFixture(t)
	ctx := context.Background()
	c := SignalContext{Key: "date-only", Kind: "location", CalendarDate: "2026-10-09", Timezone: "unknown", Location: &SignalLocation{Type: "gps", Latitude: 0, Longitude: 0}}
	if _, err := s.IngestSignals(ctx, id, SignalBatch{Source: "smartcar", BatchID: "date-only", Contexts: []SignalContext{c}}); err != nil {
		t.Fatal(err)
	}
	if location, err := s.LatestLocation(ctx, id); err != nil || location != nil {
		t.Fatal("calendar position acquired a fabricated capture time", err)
	}
}
