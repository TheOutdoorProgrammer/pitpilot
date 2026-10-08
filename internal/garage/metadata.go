package garage

import (
	"errors"
	"time"
)

type Source struct {
	System     string `json:"system"`
	Instance   string `json:"instance"`
	Collection string `json:"collection"`
	ID         string `json:"id"`
}

type ExtraField struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	IsRequired bool   `json:"isRequired"`
	FieldType  int    `json:"fieldType"`
}

type PlanDetails struct {
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	RecordKind  string   `json:"recordKind"`
	CreatedAt   string   `json:"createdAt,omitempty"`
	ModifiedAt  string   `json:"modifiedAt,omitempty"`
	ReminderIDs []string `json:"reminderIds,omitempty"`
}

type FuelDetails struct {
	FillToFull bool `json:"fillToFull"`
	MissedFill bool `json:"missedFill"`
}

type Recurrence struct {
	Miles          *float64 `json:"miles,omitempty"`
	Months         int      `json:"months,omitempty"`
	Days           int      `json:"days,omitempty"`
	FixedIntervals bool     `json:"fixedIntervals"`
}

type ReminderThresholds struct {
	UrgentDays      *int     `json:"urgentDays,omitempty"`
	VeryUrgentDays  *int     `json:"veryUrgentDays,omitempty"`
	UrgentMiles     *float64 `json:"urgentMiles,omitempty"`
	VeryUrgentMiles *float64 `json:"veryUrgentMiles,omitempty"`
}

func validateMetadata(tags []string, fields []ExtraField) error {
	if len(tags) > 100 || len(fields) > 100 {
		return errors.New("too many tags or extra fields")
	}
	for _, tag := range tags {
		if len(tag) > 200 {
			return errors.New("tag exceeds allowed length")
		}
	}
	for _, field := range fields {
		if !validTitle(field.Name) || len(field.Value) > 20000 || field.FieldType < 0 || field.FieldType > 5 {
			return errors.New("invalid extra field")
		}
	}
	return nil
}

func (r Record) validateDetails() error {
	if err := validateMetadata(r.Tags, r.ExtraFields); err != nil {
		return err
	}
	if r.InitialOdometerMiles != nil && (r.Kind != "odometer" || !bounded(*r.InitialOdometerMiles, 10000000)) {
		return errors.New("initial mileage requires an odometer record and valid mileage")
	}
	if r.OdometerStatus != "" && r.OdometerStatus != "unknown" && r.OdometerStatus != "measured" && r.OdometerStatus != "estimated" {
		return errors.New("invalid odometer status")
	}
	if r.Fuel != nil && r.Kind != "fuel" {
		return errors.New("fuel details require a fuel record")
	}
	if r.Kind == "plan" && r.Plan == nil {
		return errors.New("planned work requires plan details")
	}
	if r.Plan != nil {
		if r.Kind != "plan" {
			return errors.New("plan details require planned work")
		}
		switch r.Plan.Status {
		case "planned", "in-progress", "testing", "blocked", "done":
		default:
			return errors.New("invalid plan status")
		}
		switch r.Plan.Priority {
		case "low", "normal", "high", "critical":
		default:
			return errors.New("invalid plan priority")
		}
		switch r.Plan.RecordKind {
		case "service", "repair", "upgrade", "fuel", "expense", "note", "odometer":
		default:
			return errors.New("invalid planned record kind")
		}
		for _, stamp := range []string{r.Plan.CreatedAt, r.Plan.ModifiedAt} {
			if stamp != "" {
				if _, err := time.Parse(time.RFC3339, stamp); err != nil {
					return errors.New("plan timestamp must be RFC3339")
				}
			}
		}
		if len(r.Plan.ReminderIDs) > 100 {
			return errors.New("too many linked reminders")
		}
		for _, id := range r.Plan.ReminderIDs {
			if len(id) == 0 || len(id) > 200 {
				return errors.New("invalid linked reminder")
			}
		}
	}
	return nil
}

func (r *Recurrence) validate(reminder Reminder) error {
	if r == nil {
		return nil
	}
	if r.Months < 0 || r.Months > 1200 || r.Days < 0 || r.Days > 36600 || (r.Months != 0 && r.Days != 0) {
		return errors.New("invalid recurrence calendar interval")
	}
	if r.Miles != nil && (!bounded(*r.Miles, 10000000) || *r.Miles == 0) {
		return errors.New("recurrence mileage must be positive")
	}
	if (reminder.DueDate != nil) != (r.Months > 0 || r.Days > 0) || (reminder.DueOdometerMiles != nil) != (r.Miles != nil) {
		return errors.New("recurrence intervals must match reminder due thresholds")
	}
	return nil
}

func (r *ReminderThresholds) validate() error {
	if r == nil {
		return nil
	}
	for _, days := range []*int{r.UrgentDays, r.VeryUrgentDays} {
		if days != nil && (*days < 0 || *days > 36600) {
			return errors.New("invalid reminder day threshold")
		}
	}
	for _, miles := range []*float64{r.UrgentMiles, r.VeryUrgentMiles} {
		if miles != nil && !bounded(*miles, 10000000) {
			return errors.New("invalid reminder mileage threshold")
		}
	}
	return nil
}

// Complete advances one occurrence. Fixed schedules remain anchored to the
// previous due value, including when that leaves another overdue occurrence.
func (r *Reminder) Complete(date *string, mileage *float64) error {
	if r.Recurrence == nil {
		r.Completed = true
		return nil
	}
	if err := r.Validate(); err != nil {
		return err
	}
	next := *r
	if r.DueDate != nil {
		if date == nil || !validDate(*date) {
			return errors.New("completionDate is required for recurring calendar reminders")
		}
		anchor := *date
		if r.Recurrence.FixedIntervals {
			anchor = *r.DueDate
		}
		t, _ := time.Parse(time.DateOnly, anchor)
		if r.Recurrence.Months > 0 {
			// .NET AddMonths clamps the day to the target month's final day.
			first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, r.Recurrence.Months, 0)
			last := first.AddDate(0, 1, -1).Day()
			t = first.AddDate(0, 0, min(t.Day(), last)-1)
		} else {
			t = t.AddDate(0, 0, r.Recurrence.Days)
		}
		due := t.Format(time.DateOnly)
		next.DueDate = &due
	}
	if r.DueOdometerMiles != nil {
		if mileage == nil || !bounded(*mileage, 10000000) {
			return errors.New("completionOdometerMiles is required for recurring mileage reminders")
		}
		anchor := *mileage
		if r.Recurrence.FixedIntervals {
			anchor = *r.DueOdometerMiles
		}
		due := anchor + *r.Recurrence.Miles
		next.DueOdometerMiles = &due
	}
	next.Completed = false
	if err := next.Validate(); err != nil {
		return err
	}
	*r = next
	return nil
}
