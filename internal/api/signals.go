package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func (s *Server) registerSignals(register func(string, http.HandlerFunc)) {
	register("POST /api/v1/vehicles/{id}/signals", s.ingestSignals)
	register("GET /api/v1/vehicles/{id}/signals/latest", s.latestSignals)
	register("GET /api/v1/vehicles/{id}/signals/history", s.signalHistory)
	register("GET /api/v1/signals/catalog", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, garage.SignalDefinitions()) })
}
func (s *Server) ingestSignals(w http.ResponseWriter, r *http.Request) {
	var batch garage.SignalBatch
	if !decode(w, r, &batch) || !validate(w, batch.Validate()) {
		return
	}
	report, err := s.store.IngestSignals(r.Context(), r.PathValue("id"), batch)
	if errors.Is(err, garage.ErrSignalConflict) {
		fail(w, 409, "signal identity or revision conflict")
		return
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, report)
}
func (s *Server) latestSignals(w http.ResponseWriter, r *http.Request) {
	result, err := s.store.LatestSignals(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, result)
}
func (s *Server) signalHistory(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	allowed := map[string]bool{"metric": true, "statistic": true, "source": true, "quality": true, "from": true, "to": true, "maxPoints": true}
	for k, v := range values {
		if !allowed[k] || len(v) != 1 {
			fail(w, 400, "invalid history query")
			return
		}
	}
	now := time.Now().UTC()
	q := garage.SignalHistoryQuery{Metric: values.Get("metric"), Statistic: values.Get("statistic"), Source: values.Get("source"), Quality: values.Get("quality"), From: now.Add(-7 * 24 * time.Hour), To: now, MaxPoints: 120}
	if q.Statistic == "" {
		q.Statistic = "sample"
	}
	var err error
	if raw := values.Get("from"); raw != "" {
		q.From, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			fail(w, 400, "invalid history from timestamp")
			return
		}
	}
	if raw := values.Get("to"); raw != "" {
		q.To, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			fail(w, 400, "invalid history to timestamp")
			return
		}
	}
	if raw := values.Get("maxPoints"); raw != "" {
		q.MaxPoints, err = strconv.Atoi(raw)
		if err != nil {
			fail(w, 400, "invalid history point limit")
			return
		}
	}
	if !validate(w, q.Validate()) {
		return
	}
	result, err := s.store.SignalHistory(r.Context(), r.PathValue("id"), q)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, result)
}
