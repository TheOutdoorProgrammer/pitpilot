package lubelogger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func validatePrimitives(d document) error {
	for _, key := range []string{"Make", "Model", "LicensePlate", "VIN", "ImageLocation", "MapLocation", "PurchaseDate", "SoldDate", "OdometerMultiplier", "OdometerDifference", "VehicleIdentifier", "Description", "Notes", "NoteText"} {
		if raw, ok := d[key]; ok && !bytes.Equal(raw, []byte("null")) {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return fmt.Errorf("%s must be text", key)
			}
		}
	}
	for _, key := range []string{"Pinned", "IsFillToFull", "MissedFuelUp", "IsElectric", "IsDiesel", "UseHours", "OdometerOptional", "HasOdometerAdjustment", "IsRecurring", "FixedIntervals", "UseCustomThresholds"} {
		if raw, ok := d[key]; ok {
			var b bool
			if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &b) != nil {
				return fmt.Errorf("%s must be boolean", key)
			}
		}
	}
	return nil
}

func extraFields(raw json.RawMessage) ([]garage.ExtraField, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var docs []document
	if json.Unmarshal(raw, &docs) != nil {
		return nil, errors.New("invalid extra fields")
	}
	fields := make([]garage.ExtraField, 0, len(docs))
	for _, d := range docs {
		var f garage.ExtraField
		if json.Unmarshal(d["Name"], &f.Name) != nil || json.Unmarshal(d["Value"], &f.Value) != nil || json.Unmarshal(d["IsRequired"], &f.IsRequired) != nil {
			return nil, errors.New("invalid extra field values")
		}
		t, e := d.enum("FieldType", []string{"Text", "Number", "Decimal", "Date", "Time", "Location"})
		if e != nil {
			return nil, errors.New("invalid extra field type")
		}
		f.FieldType = t
		fields = append(fields, f)
	}
	return fields, nil
}

func validateRelationships(items []garage.ImportItem) error {
	reminders := map[string]string{}
	for _, item := range items {
		if item.Kind == "reminder" {
			reminders[item.ID] = item.VehicleID
		}
	}
	for _, item := range items {
		if item.Kind != "record" {
			continue
		}
		var r garage.Record
		_ = json.Unmarshal(item.Data, &r)
		if r.Plan == nil {
			continue
		}
		for _, id := range r.Plan.ReminderIDs {
			if reminders[id] != r.VehicleID {
				return errors.New("planned work has an orphaned or cross-vehicle reminder link")
			}
		}
	}
	return nil
}
