package garage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"
)

const gpsGap = 2 * time.Minute
const gpsStop = 5 * time.Minute
const maxRecordingFixes = 20000

type RecordedLocation struct {
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	RecordedAt     time.Time `json:"recordedAt"`
	Source         string    `json:"source"`
	AccuracyMeters *float64  `json:"accuracyMeters,omitempty"`
	SpeedKPH       *float64  `json:"speedKph,omitempty"`
	CourseDegrees  *float64  `json:"courseDegrees,omitempty"`
	AltitudeMeters *float64  `json:"altitudeMeters,omitempty"`
	Satellites     *int      `json:"satellites,omitempty"`
	HDOP           *float64  `json:"hdop,omitempty"`
	FixQuality     *int      `json:"fixQuality,omitempty"`
}

func initializeAutomaticTrips(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS automatic_trips(id TEXT PRIMARY KEY, vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, recording_id TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)));
	CREATE INDEX IF NOT EXISTS automatic_trips_recording ON automatic_trips(vehicle_id,recording_id);
	CREATE INDEX IF NOT EXISTS signal_locations_recording ON signal_contexts(vehicle_id,source,json_extract(data,'$.location.recordingId'),sort_time) WHERE kind='location';
	CREATE TABLE IF NOT EXISTS gps_history_exclusions(vehicle_id TEXT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE, recording_id TEXT NOT NULL, start_time INTEGER NOT NULL, end_time INTEGER NOT NULL, PRIMARY KEY(vehicle_id,recording_id,start_time,end_time));
	CREATE TABLE IF NOT EXISTS gps_history_policy(vehicle_id TEXT PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE, deleted_before INTEGER NOT NULL);`)
	return err
}

func isGPS(c SignalContext) bool {
	return c.Kind == "location" && c.Location != nil && c.Location.Type == "gps" && c.ObservedAt != nil && c.Location.RecordingID != "" && c.Location.HDOP != nil && c.Location.FixQuality != nil && c.Location.Satellites != nil
}

func (v SignalLocation) hasNativeGPSMetadata() bool {
	return v.RecordingID != "" || v.FixQuality != nil || v.Satellites != nil || v.HDOP != nil || v.SpeedKPH != nil || v.CourseDegrees != nil || v.AltitudeMeters != nil
}

func gpsExcluded(ctx context.Context, tx *sql.Tx, vehicleID string, c SignalContext) (bool, error) {
	if !isGPS(c) {
		return false, nil
	}
	var excluded bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gps_history_policy WHERE vehicle_id=? AND deleted_before>=?) OR EXISTS(SELECT 1 FROM gps_history_exclusions WHERE vehicle_id=? AND recording_id=? AND start_time<=? AND end_time>=?)`, vehicleID, c.ObservedAt.UnixMilli(), vehicleID, c.Location.RecordingID, c.ObservedAt.UnixMilli(), c.ObservedAt.UnixMilli()).Scan(&excluded)
	return excluded, err
}

func rebuildGPSRecordings(ctx context.Context, tx *sql.Tx, vehicleID string, batch SignalBatch) error {
	if batch.Source != "pi" {
		return nil
	}
	seen := map[string]bool{}
	for _, c := range batch.Contexts {
		if isGPS(c) {
			seen[c.Location.RecordingID] = true
		}
	}
	for recording := range seen {
		if err := rebuildGPSRecording(ctx, tx, vehicleID, recording); err != nil {
			return err
		}
	}
	return nil
}

func rebuildGPSRecording(ctx context.Context, tx *sql.Tx, vehicleID, recording string) (err error) {
	ctx, done := operation(ctx, "db.gps.trips.project")
	defer func() { done(err) }()
	rows, err := tx.QueryContext(ctx, `SELECT data FROM signal_contexts WHERE vehicle_id=? AND source='pi' AND kind='location' AND json_extract(data,'$.location.recordingId')=? ORDER BY sort_time,key LIMIT ?`, vehicleID, recording, maxRecordingFixes+1)
	if err != nil {
		return err
	}
	var fixes []SignalContext
	for rows.Next() {
		var raw []byte
		var c SignalContext
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			rows.Close()
			return err
		}
		fixes = append(fixes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(fixes) > maxRecordingFixes {
		return errors.New("GPS recording exceeds its point limit")
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM automatic_trips WHERE vehicle_id=? AND recording_id=?", vehicleID, recording); err != nil {
		return err
	}
	for _, trip := range automaticTrips(vehicleID, recording, fixes) {
		// A late bridge must not recreate a deleted journey under a new first-fix ID.
		var excluded bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gps_history_exclusions WHERE vehicle_id=? AND recording_id=? AND start_time<=? AND end_time>=?)`, vehicleID, recording, trip.EndedAt.UnixMilli(), trip.StartedAt.UnixMilli()).Scan(&excluded); err != nil {
			return err
		}
		if excluded {
			continue
		}
		raw, e := json.Marshal(trip)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO automatic_trips(id,vehicle_id,recording_id,started_at,ended_at,data) VALUES(?,?,?,?,?,?)`, trip.ID, vehicleID, recording, trip.StartedAt.UnixMilli(), trip.EndedAt.UnixMilli(), string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func automaticTrips(vehicleID, recording string, fixes []SignalContext) []Trip {
	// Millisecond ordering plus full timestamp/quality/key ties gives identical
	// projections for ordered delivery, retries and later offline backfill.
	sort.Slice(fixes, func(i, j int) bool {
		a, b := fixes[i], fixes[j]
		if !a.ObservedAt.Equal(*b.ObservedAt) {
			return a.ObservedAt.Before(*b.ObservedAt)
		}
		if *a.Location.HDOP != *b.Location.HDOP {
			return *a.Location.HDOP < *b.Location.HDOP
		}
		return a.Key < b.Key
	})
	var trips []Trip
	var points []Point
	var previous *SignalContext
	var stopAt time.Time
	stopIndex := -1
	moving := false
	flush := func() {
		if moving && len(points) >= 2 {
			distance := 0.0
			for i := 1; i < len(points); i++ {
				distance += GPSDistanceMeters(points[i-1], points[i])
			}
			if distance >= 10 {
				started, ended := points[0].RecordedAt, points[len(points)-1].RecordedAt
				id := "gps_" + digest([]byte(vehicleID + "\x00" + recording + "\x00" + started.Format(time.RFC3339Nano)))[:40]
				copyPoints := append([]Point(nil), points...)
				trips = append(trips, Trip{ID: id, VehicleID: vehicleID, Title: "Recorded drive", StartedAt: started, EndedAt: ended, DistanceMiles: distance / 1609.344, Points: copyPoints, Source: "pi-gps", DistanceQuality: "derived", RecordedPointCount: len(points)})
			}
		}
		points = nil
		stopAt = time.Time{}
		stopIndex = -1
		moving = false
	}
	for i := range fixes {
		c := &fixes[i]
		p := Point{Latitude: c.Location.Latitude, Longitude: c.Location.Longitude, RecordedAt: *c.ObservedAt}
		if previous != nil {
			gap := c.ObservedAt.Sub(*previous.ObservedAt)
			if gap == 0 {
				continue
			}
			before := Point{Latitude: previous.Location.Latitude, Longitude: previous.Location.Longitude, RecordedAt: *previous.ObservedAt}
			if gap > gpsGap || GPSDistanceMeters(before, p) > math.Max(50, 400*gap.Hours()*1000) {
				flush()
				previous = nil
			}
		}
		isMoving := c.Location.SpeedKPH != nil && *c.Location.SpeedKPH >= 3
		if isMoving {
			if !stopAt.IsZero() && c.ObservedAt.Sub(stopAt) >= gpsStop {
				if stopIndex >= 0 {
					points = points[:stopIndex+1]
				}
				flush()
				previous = nil
			}
			if len(points) == 0 && previous != nil {
				points = append(points, Point{Latitude: previous.Location.Latitude, Longitude: previous.Location.Longitude, RecordedAt: *previous.ObservedAt})
			}
			points = append(points, p)
			moving = true
			stopAt = time.Time{}
			stopIndex = -1
		} else if moving {
			if stopAt.IsZero() {
				stopAt = *c.ObservedAt
				stopIndex = len(points)
			}
			points = append(points, p)
			if c.ObservedAt.Sub(stopAt) >= gpsStop {
				points = points[:stopIndex+1]
				flush()
			}
		}
		previous = c
	}
	flush()
	return trips
}

// GPS distance joins observed fixes only, with spherical geometry. It is a
// route estimate, never an odometer increment or proof of which road was used.
func GPSDistanceMeters(a, b Point) float64 {
	r := math.Pi / 180
	dLat := (b.Latitude - a.Latitude) * r
	dLon := (b.Longitude - a.Longitude) * r
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Latitude*r)*math.Cos(b.Latitude*r)*math.Pow(math.Sin(dLon/2), 2)
	return 6371000 * 2 * math.Asin(math.Sqrt(math.Min(1, h)))
}

func (s *Store) LatestLocation(ctx context.Context, vehicleID string) (out *RecordedLocation, err error) {
	ctx, done := operation(ctx, "db.gps.location")
	defer func() { done(err) }()
	// Location type existed before native recording metadata. Smartcar keeps its
	// authentic OEM timestamp and type; neither calendar days nor period bounds
	// can establish an actual last-known measurement time.
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
		SELECT source,key,sort_time,data FROM signal_contexts
		WHERE vehicle_id=? AND kind='location' AND source IN ('pi','smartcar')
		AND json_extract(data,'$.observedAt') IS NOT NULL AND sort_time<=?
		AND (source='smartcar' OR (json_extract(data,'$.location.type')='gps'
		AND json_extract(data,'$.location.recordingId') IS NOT NULL
		AND json_extract(data,'$.location.hdop') IS NOT NULL
		AND json_extract(data,'$.location.fixQuality') IS NOT NULL
		AND json_extract(data,'$.location.satellites') IS NOT NULL)))
		SELECT source,data FROM candidates WHERE sort_time=(SELECT MAX(sort_time) FROM candidates) ORDER BY source,key`, vehicleID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var source string
		if err = rows.Scan(&source, &raw); err != nil {
			return nil, err
		}
		var c SignalContext
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if c.ObservedAt == nil || c.Location == nil || (out != nil && !c.ObservedAt.After(out.RecordedAt)) {
			continue
		}
		if source == "pi" {
			source = "pi-gps"
		}
		v := c.Location
		out = &RecordedLocation{Latitude: v.Latitude, Longitude: v.Longitude, RecordedAt: *c.ObservedAt, Source: source, AccuracyMeters: v.AccuracyMeters, SpeedKPH: v.SpeedKPH, CourseDegrees: v.CourseDegrees, AltitudeMeters: v.AltitudeMeters, Satellites: v.Satellites, HDOP: v.HDOP, FixQuality: v.FixQuality}
	}
	return out, rows.Err()
}

func (s *Store) ClearGPSHistory(ctx context.Context, vehicleID string) (err error) {
	ctx, done := operation(ctx, "db.gps.clear")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var latest int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_time),0) FROM signal_contexts WHERE vehicle_id=? AND source='pi' AND kind='location' AND json_extract(data,'$.location.type')='gps' AND json_extract(data,'$.location.recordingId') IS NOT NULL`, vehicleID).Scan(&latest); err != nil {
		return err
	}
	cutoff := max(time.Now().UnixMilli(), latest)
	if _, err = tx.ExecContext(ctx, `INSERT INTO gps_history_policy(vehicle_id,deleted_before) VALUES(?,?) ON CONFLICT(vehicle_id) DO UPDATE SET deleted_before=MAX(deleted_before,excluded.deleted_before)`, vehicleID, cutoff); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM signal_contexts WHERE vehicle_id=? AND source='pi' AND kind='location' AND json_extract(data,'$.location.type')='gps' AND json_extract(data,'$.location.recordingId') IS NOT NULL`, vehicleID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM automatic_trips WHERE vehicle_id=?`, vehicleID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM gps_history_exclusions WHERE vehicle_id=?`, vehicleID); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteAutomaticTrip(ctx context.Context, tx *sql.Tx, id string) error {
	var vehicle, recording string
	var start, end int64
	if err := tx.QueryRowContext(ctx, "SELECT vehicle_id,recording_id,started_at,ended_at FROM automatic_trips WHERE id=?", id).Scan(&vehicle, &recording, &start, &end); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gps_history_exclusions(vehicle_id,recording_id,start_time,end_time) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, vehicle, recording, start, end); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM signal_contexts WHERE vehicle_id=? AND source='pi' AND kind='location' AND json_extract(data,'$.location.recordingId')=? AND sort_time>=? AND sort_time<=?`, vehicle, recording, start, end); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM automatic_trips WHERE id=?", id)
	return err
}

func (s *Store) Trips(ctx context.Context, vehicleID string, limit int, before time.Time, beforeID string) (out []Trip, err error) {
	ctx, done := operation(ctx, "db.trips.list")
	defer func() { done(err) }()
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid trip limit")
	}
	cutoff := ""
	if !before.IsZero() {
		cutoff = before.UTC().Format(time.RFC3339Nano)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM (SELECT id,data FROM entries WHERE vehicle_id=? AND kind='trip' UNION ALL SELECT id,data FROM automatic_trips WHERE vehicle_id=?) WHERE ?='' OR julianday(json_extract(data,'$.startedAt'))<julianday(?) OR (julianday(json_extract(data,'$.startedAt'))=julianday(?) AND id<?) ORDER BY julianday(json_extract(data,'$.startedAt')) DESC,id DESC LIMIT ?`, vehicleID, vehicleID, cutoff, cutoff, cutoff, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out = []Trip{}
	for rows.Next() {
		var raw []byte
		var trip Trip
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &trip); err != nil {
			return nil, err
		}
		if len(trip.Points) > 1000 {
			trip.RecordedPointCount = len(trip.Points)
			trip.RouteSimplified = true
			trip.Points = displayRoute(trip.Points, 1000)
		}
		out = append(out, trip)
	}
	return out, rows.Err()
}

func displayRoute(points []Point, limit int) []Point {
	if len(points) <= limit {
		return points
	}
	selected := map[int]bool{0: true, len(points) - 1: true}
	for i := 1; i < len(points); i++ {
		if points[i].RecordedAt.Sub(points[i-1].RecordedAt) > gpsGap {
			selected[i-1] = true
			selected[i] = true
		}
	}
	if len(selected) > limit {
		indices := make([]int, 0, len(selected))
		for i := range selected {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		selected = map[int]bool{}
		for i := 0; i < limit; i++ {
			selected[indices[i*(len(indices)-1)/(limit-1)]] = true
		}
	} else {
		for i := 0; i < limit && len(selected) < limit; i++ {
			selected[i*(len(points)-1)/(limit-1)] = true
		}
	}
	indices := make([]int, 0, len(selected))
	for i := range selected {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	out := make([]Point, 0, len(indices))
	for _, i := range indices {
		out = append(out, points[i])
	}
	return out
}

type GPSHistoryExclusion struct {
	VehicleID   string `json:"vehicleId"`
	RecordingID string `json:"recordingId"`
	StartTime   int64  `json:"startTime"`
	EndTime     int64  `json:"endTime"`
}
type GPSHistoryPolicy struct {
	VehicleID     string `json:"vehicleId"`
	DeletedBefore int64  `json:"deletedBefore"`
}

func exportGPS(ctx context.Context, tx *sql.Tx, out *Export) error {
	rows, err := tx.QueryContext(ctx, "SELECT data FROM automatic_trips ORDER BY vehicle_id,id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		var trip Trip
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &trip); err != nil {
			rows.Close()
			return err
		}
		out.AutomaticTrips = append(out.AutomaticTrips, trip)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT vehicle_id,recording_id,start_time,end_time FROM gps_history_exclusions ORDER BY vehicle_id,recording_id,start_time,end_time")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v GPSHistoryExclusion
		if err = rows.Scan(&v.VehicleID, &v.RecordingID, &v.StartTime, &v.EndTime); err != nil {
			rows.Close()
			return err
		}
		out.GPSHistoryExclusions = append(out.GPSHistoryExclusions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT vehicle_id,deleted_before FROM gps_history_policy ORDER BY vehicle_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v GPSHistoryPolicy
		if err = rows.Scan(&v.VehicleID, &v.DeletedBefore); err != nil {
			return err
		}
		out.GPSHistoryPolicies = append(out.GPSHistoryPolicies, v)
	}
	return rows.Err()
}
