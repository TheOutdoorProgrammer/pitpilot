package summarymetrics

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

type Report struct {
	garage.SignalConversionReport
	Preserved int `json:"preserved"`
}

func Convert(ctx context.Context, store *garage.Store, token string) (Report, error) {
	var report Report
	vehicles, err := store.Vehicles(ctx)
	if err != nil {
		return report, err
	}
	conversions := []garage.SignalNoteConversion{}
	for _, vehicle := range vehicles {
		records, err := store.Entries(ctx, vehicle.ID, "record")
		if err != nil {
			return report, err
		}
		for _, raw := range records {
			var record garage.Record
			if err = json.Unmarshal(raw, &record); err != nil {
				return report, err
			}
			if record.Kind != "note" {
				continue
			}
			batch, err := Parse(record)
			if errors.Is(err, ErrUnsupported) {
				report.Preserved++
				continue
			}
			if err != nil {
				return report, err
			}
			hash, err := garage.SignalNoteHash(raw)
			if err != nil {
				return report, err
			}
			conversions = append(conversions, garage.SignalNoteConversion{NoteID: record.ID, ExpectedHash: hash, Batch: batch})
		}
	}
	report.SignalConversionReport, err = store.ConvertSignalNotes(ctx, conversions, token)
	return report, err
}
