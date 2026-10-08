package api

import (
	"errors"
	"net/http"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/summarymetrics"
	"go.opentelemetry.io/otel"
)

func (s *Server) convertSummaries(apply bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			PreviewToken string `json:"previewToken"`
		}
		if !decode(w, r, &request) {
			return
		}
		if apply && request.PreviewToken == "" {
			fail(w, 422, "previewToken is required")
			return
		}
		if !apply {
			request.PreviewToken = ""
		}
		ctx, span := otel.Tracer("pitpilot/migration").Start(r.Context(), "migration.summary_metrics")
		defer span.End()
		report, err := summarymetrics.Convert(ctx, s.store, request.PreviewToken)
		if errors.Is(err, garage.ErrSignalConversionConflict) || errors.Is(err, garage.ErrSignalConflict) {
			fail(w, 409, "summary conversion changed; run preview again")
			return
		}
		if err != nil {
			s.failure(w, r, err)
			return
		}
		s.logger.InfoContext(ctx, "summary metrics reconciled", "applied", report.Applied, "converted", report.Converted, "preserved", report.Preserved)
		respond(w, 200, report)
	}
}
