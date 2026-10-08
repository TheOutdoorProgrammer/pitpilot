package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/jsonutil"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/smartcar"
)

func decodeSmartcar(w http.ResponseWriter, r *http.Request, value any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil || jsonutil.Validate(raw) != nil || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		fail(w, 400, "invalid smartcar JSON request")
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		fail(w, 400, "invalid smartcar JSON request")
		return false
	}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(value).Elem()
	for i := 0; i < typ.NumField(); i++ {
		allowed[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for key := range fields {
		if !allowed[key] {
			fail(w, 400, "invalid smartcar JSON field")
			return false
		}
	}
	if json.Unmarshal(raw, value) != nil {
		fail(w, 400, "invalid smartcar JSON request")
		return false
	}
	return true
}

func (s *Server) registerSmartcar(register func(string, http.HandlerFunc)) {
	register("GET /api/v1/integrations/smartcar", func(w http.ResponseWriter, r *http.Request) {
		if s.smartcar == nil {
			respond(w, 200, smartcar.Configuration{})
			return
		}
		respond(w, 200, s.smartcar.Configuration())
	})
	guard := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.smartcar == nil {
				fail(w, 503, "smartcar is not configured")
				return
			}
			fn(w, r)
		}
	}
	register("GET /api/v1/vehicles/{id}/smartcar", guard(s.smartcarStatus))
	register("POST /api/v1/vehicles/{id}/smartcar/sessions", guard(s.smartcarBegin))
	register("GET /api/v1/vehicles/{id}/smartcar/sessions/{session}", guard(s.smartcarSession))
	register("POST /api/v1/vehicles/{id}/smartcar/sessions/{session}/complete", guard(s.smartcarComplete))
	register("POST /api/v1/vehicles/{id}/smartcar/sessions/{session}/bind", guard(s.smartcarBind))
	register("POST /api/v1/vehicles/{id}/smartcar/adoptions", guard(s.smartcarAdopt))
	register("POST /api/v1/vehicles/{id}/smartcar/sync", guard(s.smartcarSync))
	register("DELETE /api/v1/vehicles/{id}/smartcar", guard(s.smartcarDetach))
}
func smartcarResponse(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		code, message := smartcar.PublicError(err)
		fail(w, code, message)
		return
	}
	respond(w, status, v)
}
func (s *Server) smartcarStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.smartcar.Status(r.Context(), r.PathValue("id"))
	smartcarResponse(w, 200, v, err)
}
func (s *Server) smartcarBegin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Intent string `json:"intent"`
	}
	if !decodeSmartcar(w, r, &body) {
		return
	}
	v, err := s.smartcar.Begin(r.Context(), r.PathValue("id"), body.Intent)
	smartcarResponse(w, 201, v, err)
}
func (s *Server) smartcarSession(w http.ResponseWriter, r *http.Request) {
	v, err := s.smartcar.Session(r.Context(), r.PathValue("id"), r.PathValue("session"))
	smartcarResponse(w, 200, v, err)
}
func (s *Server) smartcarComplete(w http.ResponseWriter, r *http.Request) {
	var body smartcar.Completion
	if !decodeSmartcar(w, r, &body) {
		return
	}
	v, err := s.smartcar.Complete(r.Context(), r.PathValue("id"), r.PathValue("session"), body)
	smartcarResponse(w, 200, v, err)
}
func (s *Server) smartcarBind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CandidateID string `json:"candidateId"`
	}
	if !decodeSmartcar(w, r, &body) {
		return
	}
	v, err := s.smartcar.Bind(r.Context(), r.PathValue("id"), r.PathValue("session"), body.CandidateID)
	smartcarResponse(w, 200, v, err)
}
func (s *Server) smartcarAdopt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"userId"`
	}
	if !decodeSmartcar(w, r, &body) {
		return
	}
	v, err := s.smartcar.Adopt(r.Context(), r.PathValue("id"), body.UserID)
	smartcarResponse(w, 201, v, err)
}
func (s *Server) smartcarSync(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decodeSmartcar(w, r, &body) {
		return
	}
	v, err := s.smartcar.Sync(r.Context(), r.PathValue("id"))
	smartcarResponse(w, 202, v, err)
}
func (s *Server) smartcarDetach(w http.ResponseWriter, r *http.Request) {
	if err := s.smartcar.Detach(r.Context(), r.PathValue("id")); err != nil {
		smartcarResponse(w, 0, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
