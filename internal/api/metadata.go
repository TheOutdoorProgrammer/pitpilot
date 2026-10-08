package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

// A sparse patch is applied to the stored JSON object so an older client cannot
// discard imported or newer fields that it does not understand.
func decodePatch(w http.ResponseWriter, r *http.Request, allowed map[string]bool) (map[string]json.RawMessage, bool) {
	var patch map[string]json.RawMessage
	if !decode(w, r, &patch) {
		return nil, false
	}
	if len(patch) == 0 {
		fail(w, 422, "provide at least one field to update")
		return nil, false
	}
	for key, value := range patch {
		nullable, ok := allowed[key]
		if !ok {
			fail(w, 400, "unknown or read-only patch field")
			return nil, false
		}
		if !nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			fail(w, 422, "field cannot be null")
			return nil, false
		}
	}
	return patch, true
}

func mergePatch(fields, patch map[string]json.RawMessage) {
	for key, value := range patch {
		if key == "plan" || key == "fuel" || key == "recurrence" || key == "thresholds" {
			var old, update map[string]json.RawMessage
			if json.Unmarshal(fields[key], &old) == nil && old != nil && json.Unmarshal(value, &update) == nil && update != nil {
				for name, v := range update {
					old[name] = v
				}
				value, _ = json.Marshal(old)
			}
		}
		fields[key] = value
	}
}

func strictPatch(patch map[string]json.RawMessage, target any) error {
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func decodeFields(fields map[string]json.RawMessage, value any) error {
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (s *Server) updateRecord(w http.ResponseWriter, r *http.Request) {
	patch, ok := decodePatch(w, r, map[string]bool{
		"title": false, "date": false, "notes": false, "odometerMiles": false, "costCents": false,
		"gallons": true, "tags": true, "extraFields": true, "pinned": false, "initialOdometerMiles": true,
		"odometerStatus": false, "plan": true, "fuel": true,
	})
	if !ok {
		return
	}
	if strictPatch(patch, new(garage.Record)) != nil {
		fail(w, 400, "invalid record patch")
		return
	}
	var validationErr error
	out, err := s.store.UpdateEntry(r.Context(), r.PathValue("id"), "record", func(fields map[string]json.RawMessage) error {
		mergePatch(fields, patch)
		var record garage.Record
		if err := decodeFields(fields, &record); err != nil {
			validationErr = errors.New("invalid record patch")
			return validationErr
		}
		validationErr = record.Validate()
		return validationErr
	})
	if !validate(w, validationErr) {
		return
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) updateReminder(w http.ResponseWriter, r *http.Request) {
	patch, ok := decodePatch(w, r, map[string]bool{
		"title": false, "notes": false, "dueDate": true, "dueOdometerMiles": true, "completed": false,
		"tags": true, "extraFields": true, "recurrence": true, "thresholds": true,
		"completionDate": false, "completionOdometerMiles": false,
		"expectedDueDate": false, "expectedDueOdometerMiles": false,
	})
	if !ok {
		return
	}
	var completed *bool
	var date *string
	var mileage *float64
	var expectedDate *string
	var expectedMileage *float64
	for key, target := range map[string]any{"completed": &completed, "completionDate": &date, "completionOdometerMiles": &mileage, "expectedDueDate": &expectedDate, "expectedDueOdometerMiles": &expectedMileage} {
		if raw, ok := patch[key]; ok {
			if json.Unmarshal(raw, target) != nil {
				fail(w, 422, "invalid reminder completion")
				return
			}
		}
	}
	delete(patch, "completionDate")
	delete(patch, "completionOdometerMiles")
	delete(patch, "expectedDueDate")
	delete(patch, "expectedDueOdometerMiles")
	if strictPatch(patch, new(garage.Reminder)) != nil {
		fail(w, 400, "invalid reminder patch")
		return
	}
	if (date != nil || mileage != nil || expectedDate != nil || expectedMileage != nil) && (completed == nil || !*completed) {
		fail(w, 422, "completion values require completed true")
		return
	}
	if completed != nil && *completed {
		for _, key := range []string{"dueDate", "dueOdometerMiles", "recurrence"} {
			if _, present := patch[key]; present {
				fail(w, 422, "edit the schedule separately from completing it")
				return
			}
		}
	}
	var validationErr error
	var conflict bool
	out, err := s.store.UpdateEntry(r.Context(), r.PathValue("id"), "reminder", func(fields map[string]json.RawMessage) error {
		mergePatch(fields, patch)
		var reminder garage.Reminder
		if err := decodeFields(fields, &reminder); err != nil {
			validationErr = errors.New("invalid reminder patch")
			return validationErr
		}
		validationErr = reminder.Validate()
		if validationErr != nil {
			return validationErr
		}
		if completed != nil && *completed {
			if reminder.Recurrence != nil {
				if (reminder.DueDate != nil && expectedDate == nil) || (reminder.DueOdometerMiles != nil && expectedMileage == nil) {
					validationErr = errors.New("expected due values are required for recurring completion")
					return validationErr
				}
				if !sameOptional(reminder.DueDate, expectedDate) || !sameOptional(reminder.DueOdometerMiles, expectedMileage) {
					conflict = true
					return errors.New("reminder schedule changed")
				}
			}
			validationErr = reminder.Complete(date, mileage)
			if validationErr != nil {
				return validationErr
			}
			for key, value := range map[string]any{"completed": reminder.Completed, "dueDate": reminder.DueDate, "dueOdometerMiles": reminder.DueOdometerMiles} {
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				fields[key] = raw
			}
		}
		return nil
	})
	if conflict {
		fail(w, 409, "reminder schedule changed; refresh before completing")
		return
	}
	if !validate(w, validationErr) {
		return
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func sameOptional[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
