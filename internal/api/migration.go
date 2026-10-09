package api

import (
	"errors"
	"net/http"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/lubelogger"
	"go.opentelemetry.io/otel"
)

func (s *Server) registerMigration(register func(string, http.HandlerFunc)) {
	register("POST /api/v1/migrations/receiver/preview", s.recoverReceiver(false))
	register("POST /api/v1/migrations/receiver/apply", s.recoverReceiver(true))
	register("POST /api/v1/migrations/summary-metrics/preview", s.convertSummaries(false))
	register("POST /api/v1/migrations/summary-metrics/apply", s.convertSummaries(true))
	register("POST /api/v1/migrations/lubelogger/preview", s.migrateLubeLogger(false))
	register("POST /api/v1/migrations/lubelogger/apply", s.migrateLubeLogger(true))
}
func (s *Server) migrateLubeLogger(apply bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("pitpilot/migration").Start(r.Context(), "migration.lubelogger")
		defer span.End()
		var request lubelogger.Request
		if !decode(w, r, &request) {
			return
		}
		if apply && request.PreviewToken == "" {
			fail(w, 422, "previewToken is required")
			return
		}
		batch, summary, err := lubelogger.Convert(request)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		token := ""
		if apply {
			token = request.PreviewToken
		}
		report, err := s.store.Import(ctx, batch, token)
		if errors.Is(err, garage.ErrImportSettingsChanged) {
			fail(w, 409, "source interpretation differs from the first import; use its original settings")
			return
		}
		if errors.Is(err, garage.ErrImportConflict) {
			respond(w, 409, report)
			return
		}
		if err != nil {
			s.failure(w, r, err)
			return
		}
		s.logger.InfoContext(ctx, "migration reconciled", "applied", report.Applied, "created", report.Created, "updated", report.Updated, "skipped", report.Skipped, "conflicts", report.Conflicts)
		respond(w, 200, struct {
			garage.ImportReport
			Source lubelogger.Summary `json:"source"`
		}{report, summary})
	}
}
