package garage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func ptr[T any](value T) *T { return &value }

func TestMigrationRecordSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record Record
		valid  bool
	}{
		{"undated note", Record{Kind: "note", Title: "Notes"}, true},
		{"undated service", Record{Kind: "service", Title: "Service"}, false},
		{"initial greater than final preserved", Record{Kind: "odometer", Title: "Counter reset", Date: "2026-10-08", InitialOdometerMiles: ptr(100.0), OdometerMiles: 10}, true},
		{"invalid initial", Record{Kind: "odometer", Title: "Counter", Date: "2026-10-08", InitialOdometerMiles: ptr(-1.0)}, false},
		{"unknown measurement is valid", Record{Kind: "odometer", Title: "Counter", Date: "2026-10-08", OdometerStatus: "unknown"}, true},
		{"invalid measurement status", Record{Kind: "odometer", Title: "Counter", Date: "2026-10-08", OdometerStatus: "verified-ish"}, false},
		{"plan separate from actual work", Record{Kind: "plan", Title: "Future service", CostCents: 20000, Plan: &PlanDetails{Status: "planned", Priority: "normal", RecordKind: "service"}}, true},
		{"testing plan", Record{Kind: "plan", Title: "Future service", Plan: &PlanDetails{Status: "testing", Priority: "critical", RecordKind: "repair"}}, true},
		{"plan without status", Record{Kind: "plan", Title: "Future service"}, false},
		{"plan on service", Record{Kind: "service", Title: "Service", Date: "2026-10-08", Plan: &PlanDetails{Status: "planned", Priority: "normal", RecordKind: "service"}}, false},
		{"unknown field type", Record{Kind: "note", Title: "Note", ExtraFields: []ExtraField{{Name: "Unsupported", FieldType: 6}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.record.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func TestReminderRecurrenceCompletion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reminder  Reminder
		date      *string
		mileage   *float64
		wantDate  string
		wantMiles float64
	}{
		{"flexible months clamp", Reminder{Title: "Monthly", DueDate: ptr("2026-01-01"), Recurrence: &Recurrence{Months: 1}}, ptr("2026-01-31"), nil, "2026-02-28", 0},
		{"fixed months clamp leap year", Reminder{Title: "Monthly", DueDate: ptr("2028-01-31"), Recurrence: &Recurrence{Months: 1, FixedIntervals: true}}, ptr("2028-03-15"), nil, "2028-02-29", 0},
		{"fixed days crosses year", Reminder{Title: "Daily", DueDate: ptr("2026-12-31"), Recurrence: &Recurrence{Days: 2, FixedIntervals: true}}, ptr("2027-01-01"), nil, "2027-01-02", 0},
		{"flexible miles", Reminder{Title: "Oil", DueOdometerMiles: ptr(100.0), Recurrence: &Recurrence{Miles: ptr(50.0)}}, nil, ptr(125.0), "", 175},
		{"fixed miles stays anchored", Reminder{Title: "Oil", DueOdometerMiles: ptr(100.0), Recurrence: &Recurrence{Miles: ptr(50.0), FixedIntervals: true}}, nil, ptr(200.0), "", 150},
		{"both trigger dimensions", Reminder{Title: "Oil", DueDate: ptr("2026-01-01"), DueOdometerMiles: ptr(100.0), Recurrence: &Recurrence{Months: 3, Miles: ptr(50.0)}}, ptr("2026-02-01"), ptr(200.0), "2026-05-01", 250},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.reminder.Complete(tc.date, tc.mileage); err != nil {
				t.Fatal(err)
			}
			if tc.reminder.Completed {
				t.Fatal("recurring reminder marked permanently complete")
			}
			if tc.wantDate != "" && (tc.reminder.DueDate == nil || *tc.reminder.DueDate != tc.wantDate) {
				t.Fatalf("unexpected date: %v", tc.reminder.DueDate)
			}
			if tc.wantMiles != 0 && (tc.reminder.DueOdometerMiles == nil || *tc.reminder.DueOdometerMiles != tc.wantMiles) {
				t.Fatalf("unexpected mileage: %v", tc.reminder.DueOdometerMiles)
			}
		})
	}
}

func TestInvalidCompletionDoesNotPartiallyAdvance(t *testing.T) {
	r := Reminder{Title: "Oil", DueDate: ptr("2026-01-01"), DueOdometerMiles: ptr(100.0), Recurrence: &Recurrence{Months: 3, Miles: ptr(50.0)}}
	before, _ := json.Marshal(r)
	if err := r.Complete(ptr("2026-02-01"), nil); err == nil {
		t.Fatal("missing actual mileage accepted")
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("failed completion partially changed reminder")
	}
	r.DueOdometerMiles = ptr(10000000.0)
	if err := r.Complete(ptr("2026-02-01"), ptr(10000000.0)); err == nil {
		t.Fatal("overflow accepted")
	}
}

func TestRecurrenceValidation(t *testing.T) {
	for _, recurrence := range []*Recurrence{{}, {Months: 1}, {Miles: ptr(0.0)}, {Miles: ptr(-1.0)}, {Miles: ptr(50.0), Days: 1}, {Miles: ptr(50.0), Months: 1, Days: 1}} {
		r := Reminder{Title: "Oil", DueOdometerMiles: ptr(100.0), Recurrence: recurrence}
		if err := r.Validate(); err == nil {
			t.Fatalf("invalid recurrence accepted: %+v", recurrence)
		}
	}
}

func TestAtomicEntryUpdatesPreserveUnknownDataAndRollback(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateVehicle(ctx, Vehicle{ID: "vehicle", Name: "Vehicle"}); err != nil {
		t.Fatal(err)
	}
	data := json.RawMessage(`{"id":"entry","vehicleId":"vehicle","kind":"note","title":"Original","notes":"Original notes","future":{"unknown":[1,2]},"source":{"system":"lubelogger","instance":"fixture","collection":"notes","id":"1"}}`)
	if err := s.SaveEntry(ctx, "entry", "vehicle", "record", data, true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for key, value := range map[string]string{"title": "Changed", "notes": "Changed notes"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.UpdateEntry(ctx, "entry", "record", func(fields map[string]json.RawMessage) error { fields[key], _ = json.Marshal(value); return nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	before, err := s.Entry(ctx, "entry", "record")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(before, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["title"]) != `"Changed"` || string(fields["notes"]) != `"Changed notes"` || string(fields["future"]) != `{"unknown":[1,2]}` || fields["source"] == nil {
		t.Fatalf("fields lost: %s", before)
	}
	rejected := errors.New("rejected")
	_, err = s.UpdateEntry(ctx, "entry", "record", func(fields map[string]json.RawMessage) error {
		fields["title"] = json.RawMessage(`"Wrong"`)
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	after, _ := s.Entry(ctx, "entry", "record")
	if string(before) != string(after) {
		t.Fatal("failed update persisted changes")
	}
	if _, err := s.UpdateEntry(ctx, "entry", "reminder", func(map[string]json.RawMessage) error { t.Fatal("wrong-kind callback invoked"); return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
