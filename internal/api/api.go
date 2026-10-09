package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/smartcar"
	"github.com/TheOutdoorProgrammer/pitpilot/internal/telemetry"
)

type Server struct {
	store    *garage.Store
	token    [32]byte
	logger   *slog.Logger
	smartcar *smartcar.Service
}

func New(store *garage.Store, token string, logger *slog.Logger) (http.Handler, error) {
	return NewWithOptions(store, token, logger, Options{})
}

type Options struct {
	MetricsToken string
	Smartcar     *smartcar.Service
}

func NewWithOptions(store *garage.Store, token string, logger *slog.Logger, options Options) (http.Handler, error) {
	if len(token) < 32 {
		return nil, errors.New("API token must contain at least 32 bytes")
	}
	s := &Server{store: store, token: sha256.Sum256([]byte(token)), logger: logger, smartcar: options.Smartcar}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if store.Ping(r.Context()) != nil {
			fail(w, 503, "database unavailable")
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	register := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, s.authenticate(fn)) }
	register("GET /api/v1/vehicles", s.vehicles)
	register("POST /api/v1/vehicles", s.createVehicle)
	register("GET /api/v1/vehicles/{id}", s.vehicle)
	register("PATCH /api/v1/vehicles/{id}", s.updateVehicle)
	register("DELETE /api/v1/vehicles/{id}", s.deleteVehicle)
	register("GET /api/v1/vehicles/{id}/records", s.listEntries("record"))
	register("POST /api/v1/vehicles/{id}/records", s.createRecord)
	register("PATCH /api/v1/records/{id}", s.updateRecord)
	register("DELETE /api/v1/records/{id}", s.deleteEntry("record"))
	register("GET /api/v1/vehicles/{id}/reminders", s.listEntries("reminder"))
	register("POST /api/v1/vehicles/{id}/reminders", s.createReminder)
	register("PATCH /api/v1/reminders/{id}", s.updateReminder)
	register("DELETE /api/v1/reminders/{id}", s.deleteEntry("reminder"))
	register("GET /api/v1/vehicles/{id}/trips", s.listEntries("trip"))
	register("POST /api/v1/vehicles/{id}/trips", s.createTrip)
	register("DELETE /api/v1/trips/{id}", s.deleteEntry("trip"))
	register("GET /api/v1/export", s.export)
	register("POST /api/v1/client-events", s.clientEvent)
	s.registerMigration(register)
	s.registerSignals(register)
	s.registerDevices(mux, register)
	s.registerSmartcar(register)
	s.registerPhotos(mux, register)
	if options.MetricsToken != "" {
		if len(options.MetricsToken) < 32 || options.MetricsToken == token || strings.ContainsAny(options.MetricsToken, " \t\r\n\x00") {
			return nil, errors.New("metrics token must be distinct and contain at least 32 bytes")
		}
		metricsAuth := &Server{token: sha256.Sum256([]byte(options.MetricsToken))}
		mux.Handle("GET /metrics", metricsAuth.authenticate(s.metrics))
	}
	return telemetry.HTTP(mux, logger), nil
}

func (s *Server) authenticate(next http.HandlerFunc) http.Handler {
	return s.authenticateContentType(next, "application/json")
}

func (s *Server) authenticateContentType(next http.HandlerFunc, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		digest := sha256.Sum256([]byte(provided))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(digest[:], s.token[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, 401, "authentication required")
			return
		}
		if r.Method != "GET" && r.Method != "DELETE" && strings.Split(r.Header.Get("Content-Type"), ";")[0] != contentType {
			fail(w, 415, "Content-Type must be "+contentType)
			return
		}
		next(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "invalid JSON request")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, 400, "expected one JSON document")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}
func validate(w http.ResponseWriter, err error) bool {
	if err != nil {
		fail(w, 422, err.Error())
		return false
	}
	return true
}

func (s *Server) failure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, garage.ErrNotFound) {
		fail(w, 404, "not found")
		return
	}
	s.logger.ErrorContext(r.Context(), "database operation failed")
	fail(w, 500, "unable to complete request")
}

func (s *Server) vehicles(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Vehicles(r.Context())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) vehicle(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Vehicle(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, v)
}
func (s *Server) createVehicle(w http.ResponseWriter, r *http.Request) {
	var v garage.Vehicle
	if !decode(w, r, &v) || !validate(w, v.Validate()) {
		return
	}
	if v.Source != nil {
		fail(w, 422, "source is managed by the importer")
		return
	}
	if v.PhotoRevision != "" {
		fail(w, 422, "photo revision is managed by photo uploads")
		return
	}
	v.ID = garage.NewID()
	v.CreatedAt = time.Now().UTC()
	v.Name = strings.TrimSpace(v.Name)
	if err := s.store.CreateVehicle(r.Context(), v); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, v)
}

func (s *Server) updateVehicle(w http.ResponseWriter, r *http.Request) {
	var patch struct {
		Name           *string              `json:"name"`
		Make           *string              `json:"make"`
		Model          *string              `json:"model"`
		Year           *int                 `json:"year"`
		OdometerMiles  *float64             `json:"odometerMiles"`
		OdometerStatus *string              `json:"odometerStatus"`
		VIN            *string              `json:"vin"`
		LicensePlate   *string              `json:"licensePlate"`
		Notes          *string              `json:"notes"`
		Tags           *[]string            `json:"tags"`
		ExtraFields    *[]garage.ExtraField `json:"extraFields"`
	}
	if !decode(w, r, &patch) {
		return
	}
	var validationErr error
	v, err := s.store.UpdateVehicle(r.Context(), r.PathValue("id"), patch.OdometerMiles != nil || patch.OdometerStatus != nil, func(v *garage.Vehicle) error {
		if patch.Name != nil {
			v.Name = strings.TrimSpace(*patch.Name)
		}
		if patch.Make != nil {
			v.Make = *patch.Make
		}
		if patch.Model != nil {
			v.Model = *patch.Model
		}
		if patch.Year != nil {
			v.Year = *patch.Year
		}
		if patch.OdometerMiles != nil {
			v.OdometerMiles = *patch.OdometerMiles
			v.OdometerStatus = "unknown"
		}
		if patch.OdometerStatus != nil {
			v.OdometerStatus = *patch.OdometerStatus
		}
		if patch.VIN != nil {
			v.VIN = *patch.VIN
		}
		if patch.LicensePlate != nil {
			v.LicensePlate = *patch.LicensePlate
		}
		if patch.Notes != nil {
			v.Notes = *patch.Notes
		}
		if patch.Tags != nil {
			v.Tags = *patch.Tags
		}
		if patch.ExtraFields != nil {
			v.ExtraFields = *patch.ExtraFields
		}
		validationErr = v.Validate()
		return validationErr
	})
	if !validate(w, validationErr) {
		return
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, v)
}

func (s *Server) deleteVehicle(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteVehicle(r.Context(), r.PathValue("id")); err != nil {
		s.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) hasVehicle(w http.ResponseWriter, r *http.Request) bool {
	_, err := s.store.Vehicle(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return false
	}
	return true
}
func (s *Server) listEntries(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hasVehicle(w, r) {
			return
		}
		out, err := s.store.Entries(r.Context(), r.PathValue("id"), kind)
		if err != nil {
			s.failure(w, r, err)
			return
		}
		respond(w, 200, out)
	}
}
func (s *Server) deleteEntry(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.DeleteEntry(r.Context(), r.PathValue("id"), kind); err != nil {
			s.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Server) createRecord(w http.ResponseWriter, r *http.Request) {
	var v garage.Record
	if !decode(w, r, &v) || !validate(w, v.Validate()) || !s.hasVehicle(w, r) {
		return
	}
	if v.Source != nil {
		fail(w, 422, "source is managed by the importer")
		return
	}
	v.ID = garage.NewID()
	v.VehicleID = r.PathValue("id")
	now := time.Now().UTC()
	v.CreatedAt = &now
	if err := s.store.SaveEntry(r.Context(), v.ID, v.VehicleID, "record", v, true); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, v)
}
func (s *Server) createReminder(w http.ResponseWriter, r *http.Request) {
	var v garage.Reminder
	if !decode(w, r, &v) || !validate(w, v.Validate()) || !s.hasVehicle(w, r) {
		return
	}
	if v.Source != nil {
		fail(w, 422, "source is managed by the importer")
		return
	}
	v.ID = garage.NewID()
	v.VehicleID = r.PathValue("id")
	if err := s.store.SaveEntry(r.Context(), v.ID, v.VehicleID, "reminder", v, true); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, v)
}
func (s *Server) createTrip(w http.ResponseWriter, r *http.Request) {
	var v garage.Trip
	if !decode(w, r, &v) || !validate(w, v.Validate()) || !s.hasVehicle(w, r) {
		return
	}
	v.ID = garage.NewID()
	v.VehicleID = r.PathValue("id")
	if v.Points == nil {
		v.Points = []garage.Point{}
	}
	if err := s.store.SaveEntry(r.Context(), v.ID, v.VehicleID, "trip", v, true); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, v)
}
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Export(r.Context())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pitpilot-export.json"`)
	respond(w, 200, out)
}
