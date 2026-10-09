package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/receiverhistory"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

func (s *Server) recoverReceiver(apply bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("pitpilot/migration").Start(r.Context(), "migration.receiver_recovery")
		defer span.End()
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, receiverhistory.MaxRequestBytes))
		if err != nil || jsonutil.Validate(body) != nil {
			span.SetStatus(codes.Error, "invalid receiver recovery request")
			fail(w, 400, "invalid or oversized receiver recovery request")
			return
		}
		var request garage.ReceiverRecoveryRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil {
			span.SetStatus(codes.Error, "invalid receiver recovery request")
			fail(w, 400, "invalid receiver recovery request")
			return
		}
		if apply && request.PreviewToken == "" {
			span.SetStatus(codes.Error, "missing recovery preview token")
			fail(w, 422, "previewToken is required")
			return
		}
		if !apply {
			request.PreviewToken = ""
		}
		report, err := s.store.RecoverReceiver(ctx, request)
		if err != nil {
			span.SetStatus(codes.Error, "receiver recovery failed")
			switch {
			case errors.Is(err, receiverhistory.ErrInvalid):
				fail(w, 422, "invalid receiver history; nothing was imported")
			case errors.Is(err, garage.ErrReceiverConflict), errors.Is(err, garage.ErrSignalConflict):
				fail(w, 409, "receiver recovery conflicts with retained history; run preview again")
			default:
				s.failure(w, r, err)
			}
			return
		}
		s.logger.InfoContext(ctx, "receiver history reconciled", "applied", report.Applied, "events", report.Events, "archived", report.Archived, "skipped", report.Skipped, "unknown_clock", report.UnknownClock, "samples_created", report.SamplesCreated, "samples_reused", report.SamplesReused)
		respond(w, 200, report)
	}
}
