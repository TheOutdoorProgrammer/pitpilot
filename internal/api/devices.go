package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func (s *Server) registerDevices(mux *http.ServeMux, register func(string, http.HandlerFunc)) {
	register("GET /api/v1/vehicles/{id}/devices", s.devices)
	register("POST /api/v1/vehicles/{id}/devices", s.createDevice)
	register("PATCH /api/v1/devices/{id}", s.updateDevice)
	register("DELETE /api/v1/devices/{id}", s.revokeDevice)
	mux.HandleFunc("POST /api/v1/device/enroll", s.enrollDevice)
	mux.HandleFunc("GET /api/v1/device/config", s.deviceConfig)
	mux.HandleFunc("POST /api/v1/device/heartbeat", s.deviceHeartbeat)
	mux.HandleFunc("POST /api/v1/device/signals", s.deviceSignals)
}

func deviceRequest(w http.ResponseWriter, r *http.Request, authorize bool) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == "POST" && strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return "", false
	}
	if !authorize {
		return "", true
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || len(strings.TrimPrefix(h, "Bearer ")) != 43 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		fail(w, 401, "device authentication required")
		return "", false
	}
	return strings.TrimPrefix(h, "Bearer "), true
}

func (s *Server) deviceFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, garage.ErrDeviceUnauthorized) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		fail(w, 401, "device authorization expired or revoked")
		return
	}
	if errors.Is(err, garage.ErrDeviceLimit) {
		fail(w, 409, "revoke an existing device before adding another")
		return
	}
	if errors.Is(err, garage.ErrSignalConflict) {
		fail(w, 409, "signal identity conflict")
		return
	}
	s.failure(w, r, err)
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Devices(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !garage.ValidDeviceName(input.Name) {
		fail(w, 422, "device name must contain 1 to 80 characters")
		return
	}
	out, err := s.store.CreateDevice(r.Context(), r.PathValue("id"), input.Name)
	if err != nil {
		s.deviceFailure(w, r, err)
		return
	}
	respond(w, 201, out)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AutoUpdate *bool `json:"autoUpdate"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.AutoUpdate == nil {
		fail(w, 422, "autoUpdate is required")
		return
	}
	out, err := s.store.SetDeviceAutoUpdate(r.Context(), r.PathValue("id"), *input.AutoUpdate)
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeDevice(r.Context(), r.PathValue("id")); err != nil {
		s.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) enrollDevice(w http.ResponseWriter, r *http.Request) {
	if _, ok := deviceRequest(w, r, false); !ok {
		return
	}
	var input struct {
		EnrollmentToken string `json:"enrollmentToken"`
	}
	if !decode(w, r, &input) {
		return
	}
	out, err := s.store.EnrollDevice(r.Context(), input.EnrollmentToken)
	if err != nil {
		s.deviceFailure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) deviceConfig(w http.ResponseWriter, r *http.Request) {
	token, ok := deviceRequest(w, r, true)
	if !ok {
		return
	}
	out, err := s.store.DeviceConfiguration(r.Context(), token)
	if err != nil {
		s.deviceFailure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) deviceHeartbeat(w http.ResponseWriter, r *http.Request) {
	token, ok := deviceRequest(w, r, true)
	if !ok {
		return
	}
	var input garage.DeviceHeartbeat
	if !decode(w, r, &input) || !validate(w, input.Validate()) {
		return
	}
	if err := s.store.HeartbeatDevice(r.Context(), token, input); err != nil {
		s.deviceFailure(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) deviceSignals(w http.ResponseWriter, r *http.Request) {
	token, ok := deviceRequest(w, r, true)
	if !ok {
		return
	}
	var input garage.SignalBatch
	if !decode(w, r, &input) {
		return
	}
	input.Source = "pi"
	if !validate(w, input.Validate()) {
		return
	}
	out, err := s.store.IngestDeviceSignals(r.Context(), token, input)
	if err != nil {
		s.deviceFailure(w, r, err)
		return
	}
	respond(w, 200, struct {
		BatchID string `json:"batchId"`
		garage.SignalIngestReport
	}{BatchID: input.BatchID, SignalIngestReport: out})
}
