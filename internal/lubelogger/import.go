// Package lubelogger projects a typed LiteDB export without discarding its source documents.
package lubelogger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
)

type Export struct {
	FormatVersion int                          `json:"formatVersion"`
	Collections   map[string][]json.RawMessage `json:"collections"`
}

func (e *Export) UnmarshalJSON(data []byte) error {
	if err := validateJSON(data); err != nil {
		return err
	}
	type plain Export
	return json.Unmarshal(data, (*plain)(e))
}

func validateJSON(data []byte) error {
	return jsonutil.Validate(data)
}

type Options struct {
	Source       string `json:"source"`
	Timezone     string `json:"timezone"`
	Currency     string `json:"currency"`
	DistanceUnit string `json:"distanceUnit"`
	FuelUnit     string `json:"fuelUnit"`
}
type Request struct {
	Options      Options `json:"options"`
	Export       Export  `json:"export"`
	PreviewToken string  `json:"previewToken,omitempty"`
}
type Summary struct {
	Interpretation          Options        `json:"interpretation"`
	Counts                  map[string]int `json:"counts"`
	ExcludedSecurityRecords int            `json:"excludedSecurityRecords"`
	ArchivedDefinitions     int            `json:"archivedDefinitions"`
	CostCents               int64          `json:"costCents"`
	PlannedCostCents        int64          `json:"plannedCostCents"`
	Warnings                []string       `json:"warnings"`
}

var domain = map[string]string{"vehicles": "vehicle", "servicerecords": "service", "collisionrecords": "repair", "upgraderecords": "upgrade", "gasrecords": "fuel", "taxrecords": "expense", "odometerrecords": "odometer", "notes": "note", "planrecords": "plan", "reminderrecords": "reminder", "extrafields": "archive"}
var security = map[string]bool{"userrecords": true, "tokenrecords": true, "userconfigrecords": true, "useraccessrecords": true, "userhouseholdrecords": true, "apikeyrecords": true}

// Sanitize excludes credentials before the CLI sends anything to the server.
func Sanitize(ex Export) (Export, int) {
	out := Export{FormatVersion: ex.FormatVersion, Collections: map[string][]json.RawMessage{}}
	n := 0
	for name, docs := range ex.Collections {
		if security[name] {
			n += len(docs)
			continue
		}
		out.Collections[name] = docs
	}
	return out, n
}

type document map[string]json.RawMessage

var decimalSyntax = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func (d document) str(key string) string   { var s string; _ = json.Unmarshal(d[key], &s); return s }
func (d document) boolean(key string) bool { var b bool; _ = json.Unmarshal(d[key], &b); return b }
func (d document) number(key string) (string, error) {
	b, ok := d[key]
	if !ok || bytes.Equal(b, []byte("null")) {
		return "0", nil
	}
	var wrapped map[string]string
	if json.Unmarshal(b, &wrapped) == nil {
		if len(wrapped) != 1 {
			return "", errors.New("ambiguous numeric wrapper")
		}
		for _, key := range []string{"$numberDecimal", "$numberLong", "$numberDouble"} {
			if n, ok := wrapped[key]; ok {
				return n, nil
			}
		}
		return "", errors.New("unsupported numeric value")
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s, nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) != nil {
		return "", errors.New("invalid numeric value")
	}
	return n.String(), nil
}
func (d document) num(key string) (float64, error) {
	s, e := d.number(key)
	if e != nil {
		return 0, e
	}
	return strconv.ParseFloat(s, 64)
}
func (d document) integer(key string) (int, error) {
	s, e := d.number(key)
	if e != nil {
		return 0, e
	}
	n, e := strconv.Atoi(s)
	return n, e
}
func (d document) cents(key string) (int64, error) {
	s, e := d.number(key)
	if e != nil {
		return 0, e
	}
	if !decimalSyntax.MatchString(s) {
		return 0, errors.New("invalid decimal syntax")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, errors.New("invalid decimal")
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, errors.New("cost has unsupported sub-cent precision")
	}
	return r.Num().Int64(), nil
}
func (d document) enum(key string, names []string) (int, error) {
	s := d.str(key)
	if s != "" {
		for i, n := range names {
			if strings.EqualFold(s, n) {
				return i, nil
			}
		}
	}
	n, e := d.integer(key)
	if e != nil || n < 0 || n >= len(names) {
		return 0, errors.New("unknown enum value")
	}
	return n, nil
}
func (d document) instant(key string) (time.Time, error) {
	var wrapped struct {
		Date string `json:"$date"`
	}
	if json.Unmarshal(d[key], &wrapped) != nil || wrapped.Date == "" {
		return time.Time{}, errors.New("missing typed date")
	}
	return time.Parse(time.RFC3339Nano, wrapped.Date)
}
func identity(b json.RawMessage) (string, error) {
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return "", errors.New("missing source identity")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return "", errors.New("invalid identity")
	}
	switch v.(type) {
	case string, json.Number:
	default:
		return "", errors.New("unsupported identity type")
	}
	out, _ := json.Marshal(v)
	return string(out), nil
}

func Convert(req Request) (batch garage.ImportBatch, summary Summary, err error) {
	summary = Summary{Interpretation: req.Options, Counts: map[string]int{}, Warnings: []string{}}
	o := req.Options
	if strings.TrimSpace(o.Source) == "" || len(o.Source) > 100 || strings.ContainsAny(o.Source, "\r\n\t") {
		return batch, summary, errors.New("a stable source name is required")
	}
	if o.Currency != "USD" || o.DistanceUnit != "mi" || o.FuelUnit != "us-gal" {
		return batch, summary, errors.New("this importer currently requires USD, mi and us-gal; do not relabel other units")
	}
	if o.Timezone == "" || o.Timezone == "Local" {
		return batch, summary, errors.New("an explicit source timezone is required; host-dependent Local is unsupported")
	}
	zone, e := time.LoadLocation(o.Timezone)
	if e != nil {
		return batch, summary, errors.New("invalid source timezone")
	}
	if req.Export.FormatVersion != 1 {
		return batch, summary, errors.New("unsupported LiteDB export version")
	}
	if len(req.Export.Collections["vehicles"]) == 0 {
		return batch, summary, errors.New("source contains no vehicles")
	}
	batch.Source = o.Source
	batch.Settings, _ = json.Marshal(struct {
		Options
		FormatVersion int `json:"formatVersion"`
	}{o, req.Export.FormatVersion})
	keys := make([]string, 0, len(req.Export.Collections))
	for k := range req.Export.Collections {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vehicles := map[string]string{}
	odometers := map[string]float64{}
	odometerStatus := map[string]string{}
	for _, raw := range req.Export.Collections["vehicles"] {
		var d document
		if json.Unmarshal(raw, &d) != nil {
			return batch, summary, errors.New("invalid vehicle document")
		}
		id, e := identity(d["_id"])
		if e != nil {
			return batch, summary, e
		}
		vehicles[id] = garage.ImportID(o.Source, "vehicles", id)
	}
	for _, collection := range keys {
		docs := req.Export.Collections[collection]
		if security[collection] {
			summary.ExcludedSecurityRecords += len(docs)
			continue
		}
		kind, known := domain[collection]
		if !known && len(docs) > 0 {
			return batch, summary, fmt.Errorf("unsupported nonempty collection %q; migration blocked", collection)
		}
		summary.Counts[collection] = len(docs)
		for index, raw := range docs {
			item, e := convertOne(o, zone, vehicles, collection, kind, raw)
			if e != nil {
				return batch, summary, fmt.Errorf("%s document %d: %w", collection, index+1, e)
			}
			batch.Items = append(batch.Items, item)
			if kind == "archive" {
				summary.ArchivedDefinitions++
				continue
			}
			if item.Kind == "record" {
				var r garage.Record
				_ = json.Unmarshal(item.Data, &r)
				if r.Kind == "plan" {
					summary.PlannedCostCents += r.CostCents
				} else {
					summary.CostCents += r.CostCents
				}
				if r.Kind != "plan" && r.Kind != "note" && r.OdometerMiles > odometers[r.VehicleID] {
					odometers[r.VehicleID] = r.OdometerMiles
					odometerStatus[r.VehicleID] = r.OdometerStatus
				} else if r.OdometerMiles == odometers[r.VehicleID] && r.OdometerStatus == "estimated" {
					odometerStatus[r.VehicleID] = "estimated"
				}
			}
		}
	}
	for i := range batch.Items {
		item := &batch.Items[i]
		if item.Kind != "vehicle" {
			continue
		}
		var v garage.Vehicle
		_ = json.Unmarshal(item.Data, &v)
		v.OdometerMiles = odometers[v.ID]
		v.OdometerStatus = odometerStatus[v.ID]
		if v.OdometerStatus == "" {
			v.OdometerStatus = "unknown"
		}
		item.Data, _ = json.Marshal(v)
	}
	if err := validateRelationships(batch.Items); err != nil {
		return batch, summary, err
	}
	summary.Warnings = append(summary.Warnings, "Source documents are retained. Unsupported nonempty collections, attachments, units and sub-cent costs block import.", "Undated notes stay undated. Odometer readings retain their notes and custom fields; unknown measurement provenance stays unknown.", "Source removals do not delete PitPilot records. Stock vehicle images remain in the original source backup.")
	return batch, summary, nil
}

func convertOne(o Options, zone *time.Location, vehicles map[string]string, collection, kind string, raw json.RawMessage) (garage.ImportItem, error) {
	var d document
	if err := validateJSON(raw); err != nil {
		return garage.ImportItem{}, err
	}
	if json.Unmarshal(raw, &d) != nil {
		return garage.ImportItem{}, errors.New("invalid document")
	}
	if err := validatePrimitives(d); err != nil {
		return garage.ImportItem{}, err
	}
	id, e := identity(d["_id"])
	if e != nil {
		return garage.ImportItem{}, e
	}
	item := garage.ImportItem{Collection: collection, SourceID: id, ID: garage.ImportID(o.Source, collection, id), Raw: raw}
	source := &garage.Source{System: "lubelogger", Instance: o.Source, Collection: collection, ID: id}
	if kind == "archive" {
		item.Kind = "archive"
		item.Data = raw
		return item, nil
	}
	for _, key := range []string{"Files", "RequisitionHistory", "EquipmentRecordId"} {
		if b := d[key]; len(b) > 0 && !bytes.Equal(b, []byte("null")) {
			var refs []json.RawMessage
			if json.Unmarshal(b, &refs) != nil || len(refs) > 0 {
				return item, fmt.Errorf("%s references require additional migration support", key)
			}
		}
	}
	fields, e := extraFields(d["ExtraFields"])
	if e != nil {
		return item, e
	}
	var tags []string
	if b := d["Tags"]; len(b) > 0 {
		if json.Unmarshal(b, &tags) != nil {
			return item, errors.New("invalid tags")
		}
	}
	if kind == "vehicle" {
		if d.boolean("UseHours") || d.boolean("IsElectric") || d.boolean("HasOdometerAdjustment") {
			return item, errors.New("hours, electric consumption or adjusted odometers require additional unit support")
		}
		for _, key := range []string{"ImageLocation", "MapLocation"} {
			s := d.str(key)
			if s != "" && !strings.HasPrefix(s, "/defaults/") {
				return item, errors.New("custom vehicle assets require attachment migration")
			}
		}
		year, e := d.integer("Year")
		if e != nil {
			return item, e
		}
		name := strings.TrimSpace(fmt.Sprintf("%d %s %s", year, d.str("Make"), d.str("Model")))
		v := garage.Vehicle{ID: item.ID, Name: name, Make: d.str("Make"), Model: d.str("Model"), Year: year, LicensePlate: d.str("LicensePlate"), VIN: d.str("VIN"), Tags: tags, ExtraFields: fields, Source: source}
		if e = v.Validate(); e != nil {
			return item, e
		}
		item.Kind = "vehicle"
		item.Data, e = json.Marshal(v)
		return item, e
	}
	if kind == "expense" && d.boolean("IsRecurring") {
		return item, errors.New("recurring taxes require additional migration support")
	}
	vehicleID, e := identity(d["VehicleId"])
	if e != nil {
		return item, e
	}
	item.VehicleID = vehicles[vehicleID]
	if item.VehicleID == "" {
		return item, errors.New("orphaned vehicle reference")
	}
	if kind == "reminder" {
		return convertReminder(item, d, source, tags, fields, zone)
	}
	r := garage.Record{ID: item.ID, VehicleID: item.VehicleID, Kind: kind, Title: d.str("Description"), Notes: d.str("Notes"), Tags: tags, ExtraFields: fields, Source: source}
	if kind == "note" {
		r.Notes = d.str("NoteText")
		r.Pinned = d.boolean("Pinned")
	}
	if kind != "note" && kind != "plan" {
		instant, e := d.instant("Date")
		if e != nil {
			return item, e
		}
		r.Date = instant.In(zone).Format(time.DateOnly)
		r.OdometerMiles, e = d.num("Mileage")
		if e != nil {
			return item, e
		}
	}
	r.CostCents, e = d.cents("Cost")
	if e != nil {
		return item, e
	}
	if kind == "fuel" {
		r.Title = "Fuel fill-up"
		g, e := d.num("Gallons")
		if e != nil {
			return item, e
		}
		r.Gallons = &g
		r.Fuel = &garage.FuelDetails{FillToFull: d.boolean("IsFillToFull"), MissedFill: d.boolean("MissedFuelUp")}
	}
	if kind == "odometer" {
		r.Title = "Odometer reading"
		initial, e := d.num("InitialMileage")
		if e != nil {
			return item, e
		}
		r.InitialOdometerMiles = &initial
		r.OdometerStatus = "unknown"
		for _, field := range fields {
			if field.Name == "Source" && field.Value == "Estimated from recorded speed" {
				r.OdometerStatus = "estimated"
			}
		}
	}
	if kind == "plan" {
		status, e := d.enum("Progress", []string{"Backlog", "InProgress", "Testing", "Done"})
		if e != nil {
			return item, e
		}
		priority, e := d.enum("Priority", []string{"Critical", "Normal", "Low"})
		if e != nil {
			return item, e
		}
		recordKind, e := d.enum("ImportMode", []string{"ServiceRecord", "RepairRecord", "GasRecord", "TaxRecord", "UpgradeRecord"})
		if e != nil {
			return item, e
		}
		created, e := d.instant("DateCreated")
		if e != nil {
			return item, e
		}
		modified, e := d.instant("DateModified")
		if e != nil {
			return item, e
		}
		p := &garage.PlanDetails{Status: []string{"planned", "in-progress", "testing", "done"}[status], Priority: []string{"critical", "normal", "low"}[priority], RecordKind: []string{"service", "repair", "fuel", "expense", "upgrade"}[recordKind], CreatedAt: created.Format(time.RFC3339Nano), ModifiedAt: modified.Format(time.RFC3339Nano)}
		var refs []json.RawMessage
		if b := d["ReminderRecordIds"]; len(b) > 0 {
			if json.Unmarshal(b, &refs) != nil {
				return item, errors.New("invalid reminder links")
			}
		}
		legacy, e := d.integer("ReminderRecordId")
		if e != nil {
			return item, errors.New("invalid legacy reminder reference")
		}
		if legacy > 0 {
			refs = append(refs, json.RawMessage(strconv.Itoa(legacy)))
		}
		seen := map[string]bool{}
		for _, ref := range refs {
			id, e := identity(ref)
			if e != nil {
				return item, e
			}
			target := garage.ImportID(o.Source, "reminderrecords", id)
			if !seen[target] {
				p.ReminderIDs = append(p.ReminderIDs, target)
				seen[target] = true
			}
		}
		r.Plan = p
	}
	if e = r.Validate(); e != nil {
		return item, e
	}
	item.Kind = "record"
	item.Data, e = json.Marshal(r)
	return item, e
}

func convertReminder(item garage.ImportItem, d document, source *garage.Source, tags []string, fields []garage.ExtraField, zone *time.Location) (garage.ImportItem, error) {
	r := garage.Reminder{ID: item.ID, VehicleID: item.VehicleID, Title: d.str("Description"), Notes: d.str("Notes"), Tags: tags, ExtraFields: fields, Source: source}
	metric, e := d.enum("Metric", []string{"Date", "Odometer", "Both"})
	if e != nil {
		return item, e
	}
	if metric != 1 {
		instant, e := d.instant("Date")
		if e != nil {
			return item, e
		}
		date := instant.In(zone).Format(time.DateOnly)
		r.DueDate = &date
	}
	if metric != 0 {
		miles, e := d.num("Mileage")
		if e != nil {
			return item, e
		}
		r.DueOdometerMiles = &miles
	}
	if d.boolean("IsRecurring") {
		r.Recurrence = &garage.Recurrence{FixedIntervals: d.boolean("FixedIntervals")}
		if metric != 0 {
			m, e := interval(d, "ReminderMileageInterval", "CustomMileageInterval", map[string]int{"FiftyMiles": 50, "OneHundredMiles": 100, "FiveHundredMiles": 500, "OneThousandMiles": 1000, "ThreeThousandMiles": 3000, "FourThousandMiles": 4000, "FiveThousandMiles": 5000, "SevenThousandFiveHundredMiles": 7500, "TenThousandMiles": 10000, "FifteenThousandMiles": 15000, "TwentyThousandMiles": 20000, "ThirtyThousandMiles": 30000, "FortyThousandMiles": 40000, "FiftyThousandMiles": 50000, "SixtyThousandMiles": 60000, "OneHundredThousandMiles": 100000, "OneHundredFiftyThousandMiles": 150000})
			if e != nil {
				return item, e
			}
			miles := float64(m)
			r.Recurrence.Miles = &miles
		}
		if metric != 1 {
			m, e := interval(d, "ReminderMonthInterval", "CustomMonthInterval", map[string]int{"OneMonth": 1, "ThreeMonths": 3, "SixMonths": 6, "OneYear": 12, "TwoYears": 24, "ThreeYears": 36, "FiveYears": 60})
			if e != nil {
				return item, e
			}
			unit := d.str("CustomMonthIntervalUnit")
			n, unitErr := d.integer("CustomMonthIntervalUnit")
			if unit == "Months" {
				n = 1
				unitErr = nil
			}
			if unit == "Days" {
				n = 2
				unitErr = nil
			}
			other := d.str("ReminderMonthInterval") == "Other"
			if v, e := d.integer("ReminderMonthInterval"); e == nil && v == 0 {
				other = true
			}
			if other && (unitErr != nil || (n != 1 && n != 2)) {
				return item, errors.New("unknown custom reminder interval unit")
			}
			if other && n == 2 {
				r.Recurrence.Days = m
			} else {
				r.Recurrence.Months = m
			}
		}
	}
	if d.boolean("UseCustomThresholds") {
		var t document
		if json.Unmarshal(d["CustomThresholds"], &t) != nil {
			return item, errors.New("invalid reminder thresholds")
		}
		u, e := t.integer("UrgentDays")
		if e != nil {
			return item, e
		}
		v, e := t.integer("VeryUrgentDays")
		if e != nil {
			return item, e
		}
		m, e := t.num("UrgentDistance")
		if e != nil {
			return item, e
		}
		n, e := t.num("VeryUrgentDistance")
		if e != nil {
			return item, e
		}
		r.Thresholds = &garage.ReminderThresholds{UrgentDays: &u, VeryUrgentDays: &v, UrgentMiles: &m, VeryUrgentMiles: &n}
	}
	if e = r.Validate(); e != nil {
		return item, e
	}
	item.Kind = "reminder"
	item.Data, e = json.Marshal(r)
	return item, e
}
func interval(d document, key, custom string, names map[string]int) (int, error) {
	if s := d.str(key); s != "" && s != "Other" {
		if n, ok := names[s]; ok {
			return n, nil
		}
		return 0, errors.New("unknown reminder interval")
	}
	n, e := d.integer(key)
	if d.str(key) == "Other" {
		n = 0
		e = nil
	}
	if e != nil {
		return 0, e
	}
	if n == 0 {
		return d.integer(custom)
	}
	return n, nil
}
