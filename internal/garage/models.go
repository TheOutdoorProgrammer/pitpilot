package garage

import (
	"errors"
	"math"
	"strings"
	"time"
)

type Vehicle struct {
	ID                        string       `json:"id"`
	Name                      string       `json:"name"`
	Make                      string       `json:"make"`
	Model                     string       `json:"model"`
	Year                      int          `json:"year"`
	OdometerMiles             float64      `json:"odometerMiles"`
	OdometerStatus            string       `json:"odometerStatus,omitempty"`
	OdometerExcludedIntervals int          `json:"odometerExcludedIntervals,omitempty"`
	CreatedAt                 time.Time    `json:"createdAt"`
	VIN                       string       `json:"vin,omitempty"`
	LicensePlate              string       `json:"licensePlate,omitempty"`
	Notes                     string       `json:"notes,omitempty"`
	Tags                      []string     `json:"tags,omitempty"`
	ExtraFields               []ExtraField `json:"extraFields,omitempty"`
	Source                    *Source      `json:"source,omitempty"`
}

type Record struct {
	ID                   string       `json:"id"`
	VehicleID            string       `json:"vehicleId"`
	Kind                 string       `json:"kind"`
	Date                 string       `json:"date"`
	RecordedAt           *time.Time   `json:"recordedAt,omitempty"`
	CreatedAt            *time.Time   `json:"createdAt,omitempty"`
	Title                string       `json:"title"`
	Notes                string       `json:"notes"`
	OdometerMiles        float64      `json:"odometerMiles"`
	CostCents            int64        `json:"costCents"`
	Gallons              *float64     `json:"gallons"`
	Tags                 []string     `json:"tags,omitempty"`
	ExtraFields          []ExtraField `json:"extraFields,omitempty"`
	Pinned               bool         `json:"pinned,omitempty"`
	InitialOdometerMiles *float64     `json:"initialOdometerMiles,omitempty"`
	OdometerStatus       string       `json:"odometerStatus,omitempty"`
	Plan                 *PlanDetails `json:"plan,omitempty"`
	Fuel                 *FuelDetails `json:"fuel,omitempty"`
	Source               *Source      `json:"source,omitempty"`
}

type Reminder struct {
	ID               string              `json:"id"`
	VehicleID        string              `json:"vehicleId"`
	Title            string              `json:"title"`
	DueDate          *string             `json:"dueDate"`
	DueOdometerMiles *float64            `json:"dueOdometerMiles"`
	Completed        bool                `json:"completed"`
	Notes            string              `json:"notes,omitempty"`
	Tags             []string            `json:"tags,omitempty"`
	ExtraFields      []ExtraField        `json:"extraFields,omitempty"`
	Recurrence       *Recurrence         `json:"recurrence,omitempty"`
	Thresholds       *ReminderThresholds `json:"thresholds,omitempty"`
	Source           *Source             `json:"source,omitempty"`
}

type Point struct {
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	RecordedAt time.Time `json:"recordedAt"`
}

type Trip struct {
	ID            string    `json:"id"`
	VehicleID     string    `json:"vehicleId"`
	Title         string    `json:"title"`
	StartedAt     time.Time `json:"startedAt"`
	EndedAt       time.Time `json:"endedAt"`
	DistanceMiles float64   `json:"distanceMiles"`
	Points        []Point   `json:"points"`
}

func bounded(v float64, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= max
}
func validTitle(v string) bool { return strings.TrimSpace(v) != "" && len(v) <= 200 }
func validDate(v string) bool  { _, err := time.Parse(time.DateOnly, v); return err == nil }

func (v Vehicle) Validate() error {
	if v.OdometerStatus != "" && v.OdometerStatus != "unknown" && v.OdometerStatus != "measured" && v.OdometerStatus != "estimated" {
		return errors.New("invalid odometer status")
	}
	if !validTitle(v.Name) || len(v.Make) > 100 || len(v.Model) > 100 {
		return errors.New("name is required; vehicle text exceeds allowed length")
	}
	if v.Year != 0 && (v.Year < 1886 || v.Year > time.Now().Year()+2) {
		return errors.New("invalid vehicle year")
	}
	if !bounded(v.OdometerMiles, 10000000) {
		return errors.New("invalid odometer mileage")
	}
	if len(v.VIN) > 100 || len(v.LicensePlate) > 100 || len(v.Notes) > 20000 {
		return errors.New("vehicle metadata exceeds allowed length")
	}
	return validateMetadata(v.Tags, v.ExtraFields)
}

func (r Record) Validate() error {
	switch r.Kind {
	case "service", "repair", "upgrade", "fuel", "expense", "note", "odometer", "plan":
	default:
		return errors.New("invalid record kind")
	}
	if !validTitle(r.Title) || len(r.Notes) > 20000 {
		return errors.New("title is required; record text exceeds allowed length")
	}
	if !validDate(r.Date) && !(r.Date == "" && (r.Kind == "note" || r.Kind == "plan")) {
		return errors.New("date must be YYYY-MM-DD")
	}
	if r.RecordedAt != nil && (!signalTime(r.RecordedAt) || r.RecordedAt.Format(time.DateOnly) != r.Date || r.RecordedAt.After(time.Now().Add(5*time.Minute))) {
		return errors.New("recordedAt must be a real capture time on the record date, not in the future")
	}
	if !bounded(r.OdometerMiles, 10000000) || r.CostCents < 0 || r.CostCents > 100000000000 {
		return errors.New("invalid mileage or cost")
	}
	if r.Gallons != nil && (!bounded(*r.Gallons, 10000) || *r.Gallons == 0 || r.Kind != "fuel") {
		return errors.New("gallons must be positive and belong to a fuel record")
	}
	return r.validateDetails()
}

func (r Reminder) Validate() error {
	if !validTitle(r.Title) {
		return errors.New("title is required and must be at most 200 bytes")
	}
	if r.DueDate == nil && r.DueOdometerMiles == nil {
		return errors.New("provide a due date or mileage")
	}
	if r.DueDate != nil && !validDate(*r.DueDate) {
		return errors.New("dueDate must be YYYY-MM-DD")
	}
	if r.DueOdometerMiles != nil && !bounded(*r.DueOdometerMiles, 10000000) {
		return errors.New("invalid due mileage")
	}
	if len(r.Notes) > 20000 {
		return errors.New("reminder notes exceed allowed length")
	}
	if err := validateMetadata(r.Tags, r.ExtraFields); err != nil {
		return err
	}
	if err := r.Recurrence.validate(r); err != nil {
		return err
	}
	return r.Thresholds.validate()
}

func (t Trip) Validate() error {
	if !validTitle(t.Title) || t.StartedAt.IsZero() || t.EndedAt.Before(t.StartedAt) {
		return errors.New("title and ordered trip timestamps are required")
	}
	if t.EndedAt.Sub(t.StartedAt) > 31*24*time.Hour || !bounded(t.DistanceMiles, 100000) || len(t.Points) > 20000 {
		return errors.New("trip exceeds recording limits")
	}
	last := t.StartedAt
	for _, p := range t.Points {
		if math.IsNaN(p.Latitude) || math.IsNaN(p.Longitude) || math.Abs(p.Latitude) > 90 || math.Abs(p.Longitude) > 180 || p.RecordedAt.Before(last) || p.RecordedAt.After(t.EndedAt) {
			return errors.New("invalid coordinates or unordered recording timestamps")
		}
		last = p.RecordedAt
	}
	return nil
}
