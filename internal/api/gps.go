package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func (s *Server) location(w http.ResponseWriter, r *http.Request) {
	if !s.hasVehicle(w, r) {
		return
	}
	location, err := s.store.LatestLocation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, struct {
		Location *garage.RecordedLocation `json:"location"`
	}{location})
}
func (s *Server) clearLocationHistory(w http.ResponseWriter, r *http.Request) {
	if !s.hasVehicle(w, r) {
		return
	}
	if err := s.store.ClearGPSHistory(r.Context(), r.PathValue("id")); err != nil {
		s.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) trips(w http.ResponseWriter, r *http.Request) {
	if !s.hasVehicle(w, r) {
		return
	}
	query := r.URL.Query()
	for k, v := range query {
		if (k != "limit" && k != "before" && k != "beforeId") || len(v) != 1 {
			fail(w, 400, "invalid trip query")
			return
		}
	}
	limit := 50
	if value := query.Get("limit"); value != "" {
		v, err := strconv.Atoi(value)
		if err != nil || v < 1 || v > 100 {
			fail(w, 400, "limit must be between 1 and 100")
			return
		}
		limit = v
	}
	var before time.Time
	if value := query.Get("before"); value != "" {
		v, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			fail(w, 400, "before must be a timestamp")
			return
		}
		before = v
	}
	beforeID := query.Get("beforeId")
	if len(beforeID) > 128 || before.IsZero() && beforeID != "" {
		fail(w, 400, "invalid trip cursor")
		return
	}
	trips, err := s.store.Trips(r.Context(), r.PathValue("id"), limit, before, beforeID)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, trips)
}
