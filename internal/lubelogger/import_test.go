package lubelogger

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func syntheticJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func syntheticRequest(t *testing.T) Request {
	t.Helper()
	return Request{Options: Options{Source: "synthetic-source", Timezone: "America/New_York", Currency: "USD", DistanceUnit: "mi", FuelUnit: "us-gal"}, Export: Export{FormatVersion: 1, Collections: map[string][]json.RawMessage{
		"vehicles":        {json.RawMessage(`{"_id":1,"Year":2015,"Make":"Fixture","Model":"Truck","LicensePlate":"SYNTHETIC","UseHours":false,"IsElectric":false,"HasOdometerAdjustment":false,"ImageLocation":"/defaults/truck.png","Tags":["test"],"ExtraFields":[{"Name":"VIN","Value":"synthetic-only","IsRequired":false,"FieldType":"Text"}]}`)},
		"gasrecords":      {json.RawMessage(`{"_id":1,"VehicleId":1,"Date":{"$date":"2026-01-01T02:30:00Z"},"Mileage":1000,"Gallons":{"$numberDecimal":"3.125"},"Cost":{"$numberDecimal":"34.5600"},"IsFillToFull":false,"MissedFuelUp":true,"Notes":"Synthetic fuel","Files":[],"UnknownNested":{"Value":{"$numberDecimal":"1.234567890123456789"}}}`)},
		"servicerecords":  {json.RawMessage(`{"_id":1,"VehicleId":1,"Date":{"$date":"2026-02-01T12:00:00Z"},"Mileage":1100,"Cost":{"$numberDecimal":"12.34"},"Description":"Synthetic service","Notes":"Exact service text","Tags":["engine"]}`)},
		"odometerrecords": {json.RawMessage(`{"_id":1,"VehicleId":1,"Date":{"$date":"2026-02-02T12:00:00Z"},"InitialMileage":1100,"Mileage":1112,"Notes":"Synthetic sample","ExtraFields":[{"Name":"Source","Value":"Estimated from recorded speed","IsRequired":false,"FieldType":"Text"},{"Name":"Precision","Value":"001.2300","IsRequired":false,"FieldType":"Decimal"}]}`)},
		"notes":           {json.RawMessage(`{"_id":1,"VehicleId":1,"Description":"Synthetic note","NoteText":"Line one\nLine two\n","Pinned":true,"Tags":["tag one","tag-two"],"Files":[]}`)},
		"reminderrecords": {json.RawMessage(`{"_id":1,"VehicleId":1,"Description":"Synthetic reminder","Notes":"Keep this","Metric":"Odometer","Mileage":5000,"IsRecurring":false,"UseCustomThresholds":false}`)},
		"planrecords":     {json.RawMessage(`{"_id":1,"VehicleId":1,"Description":"Synthetic repair plan","Notes":"Not yet performed","ImportMode":"RepairRecord","Priority":"Normal","Progress":"Backlog","Cost":{"$numberDecimal":"78.90"},"DateCreated":{"$date":"2026-01-02T03:04:05Z"},"DateModified":{"$date":"2026-01-03T03:04:05Z"},"ReminderRecordIds":[1],"ReminderRecordId":1}`)},
		"extrafields":     {json.RawMessage(`{"_id":6,"ExtraFields":[{"Name":"Source","FieldType":"Text","IsRequired":false}]}`)},
	}}}
}

func changeSynthetic(t *testing.T, req *Request, collection, key string, value any) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(req.Export.Collections[collection][0], &fields); err != nil {
		t.Fatal(err)
	}
	fields[key] = syntheticJSON(t, value)
	req.Export.Collections[collection][0] = syntheticJSON(t, fields)
}

func convertedItem(t *testing.T, batch garage.ImportBatch, collection string) garage.ImportItem {
	t.Helper()
	for _, item := range batch.Items {
		if item.Collection == collection {
			return item
		}
	}
	t.Fatalf("missing collection %s", collection)
	return garage.ImportItem{}
}

func TestConvertRetainsTypedSourceAndDomainSemantics(t *testing.T) {
	req := syntheticRequest(t)
	batch, summary, err := Convert(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 8 || summary.CostCents != 4690 || summary.PlannedCostCents != 7890 || summary.ArchivedDefinitions != 1 {
		t.Fatalf("reconciliation mismatch: %+v", summary)
	}
	for collection, docs := range req.Export.Collections {
		item := convertedItem(t, batch, collection)
		if !bytes.Equal(item.Raw, docs[0]) {
			t.Fatalf("raw %s changed", collection)
		}
		if summary.Counts[collection] != len(docs) {
			t.Fatalf("count mismatch: %s", collection)
		}
	}
	var fuel garage.Record
	if err := json.Unmarshal(convertedItem(t, batch, "gasrecords").Data, &fuel); err != nil {
		t.Fatal(err)
	}
	if fuel.Date != "2025-12-31" || fuel.CostCents != 3456 || fuel.Gallons == nil || *fuel.Gallons != 3.125 || fuel.Fuel == nil || fuel.Fuel.FillToFull || !fuel.Fuel.MissedFill {
		t.Fatalf("fuel projection: %+v", fuel)
	}
	var note garage.Record
	if err := json.Unmarshal(convertedItem(t, batch, "notes").Data, &note); err != nil {
		t.Fatal(err)
	}
	if note.Date != "" || note.Notes != "Line one\nLine two\n" || !note.Pinned || !reflect.DeepEqual(note.Tags, []string{"tag one", "tag-two"}) || note.Source == nil || note.Source.Collection != "notes" {
		t.Fatalf("note projection: %+v", note)
	}
	var odometer garage.Record
	if err := json.Unmarshal(convertedItem(t, batch, "odometerrecords").Data, &odometer); err != nil {
		t.Fatal(err)
	}
	if odometer.InitialOdometerMiles == nil || *odometer.InitialOdometerMiles != 1100 || odometer.OdometerMiles != 1112 || odometer.OdometerStatus != "estimated" || odometer.ExtraFields[1].Value != "001.2300" || odometer.ExtraFields[1].FieldType != 2 {
		t.Fatalf("odometer projection: %+v", odometer)
	}
	var plan garage.Record
	if err := json.Unmarshal(convertedItem(t, batch, "planrecords").Data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "plan" || plan.Date != "" || plan.Plan == nil || plan.Plan.Status != "planned" || plan.Plan.Priority != "normal" || plan.Plan.RecordKind != "repair" || plan.Plan.CreatedAt != "2026-01-02T03:04:05Z" || len(plan.Plan.ReminderIDs) != 1 {
		t.Fatalf("plan projection: %+v", plan)
	}
	if plan.Plan.ReminderIDs[0] != convertedItem(t, batch, "reminderrecords").ID {
		t.Fatal("linked reminder identity lost")
	}
	var vehicle garage.Vehicle
	if err := json.Unmarshal(convertedItem(t, batch, "vehicles").Data, &vehicle); err != nil {
		t.Fatal(err)
	}
	if vehicle.OdometerMiles != 1112 || vehicle.ExtraFields[0].FieldType != 0 {
		t.Fatalf("vehicle projection: %+v", vehicle)
	}
}

func TestConvertExtraFieldEnumRepresentations(t *testing.T) {
	for index, name := range []string{"Text", "Number", "Decimal", "Date", "Time", "Location"} {
		for _, representation := range []any{index, name} {
			req := syntheticRequest(t)
			changeSynthetic(t, &req, "notes", "ExtraFields", []map[string]any{{"Name": "Exact field", "Value": "unchanged value", "IsRequired": true, "FieldType": representation}})
			batch, _, err := Convert(req)
			if err != nil {
				t.Fatalf("enum %v: %v", representation, err)
			}
			var record garage.Record
			if err := json.Unmarshal(convertedItem(t, batch, "notes").Data, &record); err != nil {
				t.Fatal(err)
			}
			if len(record.ExtraFields) != 1 || record.ExtraFields[0].FieldType != index || record.ExtraFields[0].Value != "unchanged value" || !record.ExtraFields[0].IsRequired {
				t.Fatalf("enum field lost: %+v", record.ExtraFields)
			}
		}
	}
}

func TestConvertRejectsMalformedAndUnsupportedFields(t *testing.T) {
	for _, tc := range []struct {
		name, collection, key string
		value                 any
	}{
		{"note is number", "notes", "NoteText", 123},
		{"pinned is string", "notes", "Pinned", "true"},
		{"recurring is string", "reminderrecords", "IsRecurring", "true"},
		{"recurring is null", "reminderrecords", "IsRecurring", nil},
		{"electric flag is number", "vehicles", "IsElectric", 1},
		{"date untyped", "servicerecords", "Date", "2026-02-01"},
		{"date invalid", "servicerecords", "Date", map[string]string{"$date": "not-a-date"}},
		{"subcent cost", "gasrecords", "Cost", map[string]string{"$numberDecimal": "0.001"}},
		{"rational is not decimal", "gasrecords", "Cost", map[string]string{"$numberDecimal": "1/2"}},
		{"orphan vehicle", "notes", "VehicleId", 99},
		{"orphan reminder", "planrecords", "ReminderRecordIds", []int{99}},
		{"malformed legacy reminder", "planrecords", "ReminderRecordId", "invalid"},
		{"unknown metric", "reminderrecords", "Metric", "OdometerMaybe"},
		{"unknown plan status", "planrecords", "Progress", "Almost"},
		{"unknown priority", "planrecords", "Priority", 99},
		{"unknown extra field type", "notes", "ExtraFields", []map[string]any{{"Name": "Field", "Value": "Value", "IsRequired": false, "FieldType": "Secret"}}},
		{"invalid extra field boolean", "notes", "ExtraFields", []map[string]any{{"Name": "Field", "Value": "Value", "IsRequired": "false", "FieldType": "Text"}}},
		{"attachment requires copy", "notes", "Files", []map[string]string{{"Name": "receipt.pdf", "Location": "/documents/receipt.pdf"}}},
		{"custom image requires copy", "vehicles", "ImageLocation", "/images/vehicle.jpg"},
		{"engine hours", "vehicles", "UseHours", true},
		{"electric energy", "vehicles", "IsElectric", true},
		{"odometer adjustment", "vehicles", "HasOdometerAdjustment", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := syntheticRequest(t)
			changeSynthetic(t, &req, tc.collection, tc.key, tc.value)
			if _, _, err := Convert(req); err == nil {
				t.Fatal("unsupported or malformed source was accepted")
			}
		})
	}
}

func TestConvertBlocksUnsupportedUnitsCollectionsAndRecurringTaxes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request)
	}{
		{"currency", func(r *Request) { r.Options.Currency = "EUR" }},
		{"distance", func(r *Request) { r.Options.DistanceUnit = "km" }},
		{"fuel", func(r *Request) { r.Options.FuelUnit = "liters" }},
		{"timezone missing", func(r *Request) { r.Options.Timezone = "" }},
		{"timezone host dependent", func(r *Request) { r.Options.Timezone = "Local" }},
		{"timezone invalid", func(r *Request) { r.Options.Timezone = "Not/AZone" }},
		{"version", func(r *Request) { r.Export.FormatVersion = 2 }},
		{"unknown records", func(r *Request) { r.Export.Collections["newrecords"] = []json.RawMessage{json.RawMessage(`{"_id":1}`)} }},
		{"recurring tax", func(r *Request) {
			r.Export.Collections["taxrecords"] = []json.RawMessage{json.RawMessage(`{"_id":1,"VehicleId":1,"Description":"Annual registration","Date":{"$date":"2026-01-01T12:00:00Z"},"Cost":{"$numberDecimal":"25.00"},"IsRecurring":true}`)}
		}},
		{"cross vehicle reminder", func(r *Request) {
			var v map[string]any
			_ = json.Unmarshal(r.Export.Collections["vehicles"][0], &v)
			v["_id"] = 2
			r.Export.Collections["vehicles"] = append(r.Export.Collections["vehicles"], syntheticJSON(t, v))
			changeSynthetic(t, r, "reminderrecords", "VehicleId", 2)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := syntheticRequest(t)
			tc.change(&req)
			if _, _, err := Convert(req); err == nil {
				t.Fatal("unsafe projection accepted")
			}
		})
	}
	req := syntheticRequest(t)
	req.Export.Collections["future-empty-collection"] = []json.RawMessage{}
	if _, _, err := Convert(req); err != nil {
		t.Fatalf("empty unknown collection should be harmless: %v", err)
	}
}

func TestConvertRecurringReminderIntervals(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		metric               any
		interval             any
		custom               int
		unit                 any
		wantMonths, wantDays int
	}{
		{"named month", "Date", "ThreeMonths", 0, "Months", 3, 0},
		{"numeric month", 0, 3, 0, 1, 3, 0},
		{"custom days", "Date", "Other", 14, "Days", 0, 14},
		{"numeric custom days", 0, 0, 14, 2, 0, 14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := syntheticRequest(t)
			for key, value := range map[string]any{"Metric": tc.metric, "IsRecurring": true, "Date": map[string]string{"$date": "2026-01-01T12:00:00Z"}, "ReminderMonthInterval": tc.interval, "CustomMonthInterval": tc.custom, "CustomMonthIntervalUnit": tc.unit, "FixedIntervals": true} {
				changeSynthetic(t, &req, "reminderrecords", key, value)
			}
			batch, _, err := Convert(req)
			if err != nil {
				t.Fatal(err)
			}
			var reminder garage.Reminder
			if err := json.Unmarshal(convertedItem(t, batch, "reminderrecords").Data, &reminder); err != nil {
				t.Fatal(err)
			}
			if reminder.Recurrence == nil || reminder.Recurrence.Months != tc.wantMonths || reminder.Recurrence.Days != tc.wantDays || !reminder.Recurrence.FixedIntervals || reminder.DueOdometerMiles != nil {
				t.Fatalf("recurrence changed: %+v", reminder)
			}
		})
	}
	req := syntheticRequest(t)
	for key, value := range map[string]any{"Metric": "Date", "IsRecurring": true, "Date": map[string]string{"$date": "2026-01-01T12:00:00Z"}, "ReminderMonthInterval": "Other", "CustomMonthInterval": 14, "CustomMonthIntervalUnit": "Weeks"} {
		changeSynthetic(t, &req, "reminderrecords", key, value)
	}
	if _, _, err := Convert(req); err == nil {
		t.Fatal("unknown custom interval unit accepted")
	}
}

func TestSanitizeAndConvertExcludeSecurityCollections(t *testing.T) {
	req := syntheticRequest(t)
	for _, collection := range []string{"userrecords", "tokenrecords", "userconfigrecords", "useraccessrecords", "userhouseholdrecords", "apikeyrecords"} {
		req.Export.Collections[collection] = []json.RawMessage{json.RawMessage(`{"_id":1,"SyntheticSecret":"DO-NOT-IMPORT"}`)}
	}
	clean, excluded := Sanitize(req.Export)
	if excluded != 6 {
		t.Fatalf("excluded=%d", excluded)
	}
	data := syntheticJSON(t, clean)
	if bytes.Contains(data, []byte("DO-NOT-IMPORT")) {
		t.Fatal("credential collection survived sanitization")
	}
	if len(req.Export.Collections) != 14 {
		t.Fatal("sanitization mutated source archive")
	}
	batch, summary, err := Convert(req)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ExcludedSecurityRecords != 6 || len(batch.Items) != 8 {
		t.Fatal("security rows entered import batch")
	}
	if bytes.Contains(syntheticJSON(t, batch), []byte("DO-NOT-IMPORT")) {
		t.Fatal("private security archive entered domain tables")
	}
}

func TestExportRejectsAmbiguousJSON(t *testing.T) {
	for _, data := range []string{
		`{"formatVersion":1,"formatVersion":2,"collections":{}}`,
		`{"formatVersion":1,"collections":{"vehicles":[],"vehicles":[]}}`,
		`{"formatVersion":1,"collections":{"notes":[{"_id":1,"NoteText":"first","NoteText":"second"}]}}`,
		`{"formatVersion":1,"collections":{}} {"formatVersion":1}`,
	} {
		var export Export
		if err := json.Unmarshal([]byte(data), &export); err == nil {
			t.Fatal("ambiguous export accepted")
		}
	}
	req := syntheticRequest(t)
	req.Export.Collections["notes"][0] = json.RawMessage(strings.Replace(string(req.Export.Collections["notes"][0]), `"Pinned":true`, `"Pinned":true,"Pinned":false`, 1))
	if _, _, err := Convert(req); err == nil {
		t.Fatal("direct conversion accepted duplicate document keys")
	}
}

func TestConvertPreservesTypedIdentityAndUnknownMeasurement(t *testing.T) {
	req := syntheticRequest(t)
	var second map[string]json.RawMessage
	if err := json.Unmarshal(req.Export.Collections["vehicles"][0], &second); err != nil {
		t.Fatal(err)
	}
	second["_id"] = json.RawMessage(`"1"`)
	req.Export.Collections["vehicles"] = append(req.Export.Collections["vehicles"], syntheticJSON(t, second))
	changeSynthetic(t, &req, "notes", "VehicleId", "1")
	changeSynthetic(t, &req, "odometerrecords", "ExtraFields", []map[string]any{{"Name": "Source", "Value": "Unclassified telemetry", "IsRequired": false, "FieldType": "Text"}})
	batch, _, err := Convert(req)
	if err != nil {
		t.Fatal(err)
	}
	var numericID, stringID string
	for _, item := range batch.Items {
		if item.Collection == "vehicles" {
			switch item.SourceID {
			case "1":
				numericID = item.ID
			case `"1"`:
				stringID = item.ID
			}
		}
	}
	if numericID == "" || stringID == "" || numericID == stringID {
		t.Fatal("typed source identities collided")
	}
	if convertedItem(t, batch, "notes").VehicleID != stringID {
		t.Fatal("typed parent reference changed")
	}
	var odometer garage.Record
	if err := json.Unmarshal(convertedItem(t, batch, "odometerrecords").Data, &odometer); err != nil {
		t.Fatal(err)
	}
	if odometer.OdometerStatus != "unknown" {
		t.Fatal("unknown provenance was asserted as a measured reading")
	}
}

func TestConvertMileageRecurrenceAndCustomThresholds(t *testing.T) {
	for _, interval := range []any{"FiveThousandMiles", 5000, "Other"} {
		req := syntheticRequest(t)
		for key, value := range map[string]any{
			"IsRecurring": true, "ReminderMileageInterval": interval, "CustomMileageInterval": 5000,
			"UseCustomThresholds": true, "CustomThresholds": map[string]any{"UrgentDays": 30, "VeryUrgentDays": 7, "UrgentDistance": 500, "VeryUrgentDistance": 100},
		} {
			changeSynthetic(t, &req, "reminderrecords", key, value)
		}
		batch, _, err := Convert(req)
		if err != nil {
			t.Fatalf("interval %v: %v", interval, err)
		}
		var reminder garage.Reminder
		if err := json.Unmarshal(convertedItem(t, batch, "reminderrecords").Data, &reminder); err != nil {
			t.Fatal(err)
		}
		if reminder.Recurrence == nil || reminder.Recurrence.Miles == nil || *reminder.Recurrence.Miles != 5000 || reminder.Thresholds == nil || reminder.Thresholds.UrgentMiles == nil || *reminder.Thresholds.UrgentMiles != 500 || reminder.Thresholds.VeryUrgentDays == nil || *reminder.Thresholds.VeryUrgentDays != 7 {
			t.Fatalf("recurrence or thresholds lost: %+v", reminder)
		}
	}
}
