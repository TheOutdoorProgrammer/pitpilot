package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

func (s *Server) registerPhotos(mux *http.ServeMux, register func(string, http.HandlerFunc)) {
	register("GET /api/v1/vehicles/{id}/photo", s.vehiclePhoto)
	register("DELETE /api/v1/vehicles/{id}/photo", s.deleteVehiclePhoto)
	mux.Handle("PUT /api/v1/vehicles/{id}/photo", s.authenticateContentType(s.putVehiclePhoto, "image/jpeg"))
}

func (s *Server) vehiclePhoto(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.VehiclePhoto(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(p.Data)))
	w.Header().Set("ETag", `"`+p.Revision+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Data)
}

func (s *Server) putVehiclePhoto(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, garage.MaxVehiclePhotoBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, 413, "photo exceeds 2 MiB")
			return
		}
		fail(w, 400, "unable to read photo")
		return
	}
	v, err := s.store.PutVehiclePhoto(r.Context(), r.PathValue("id"), body)
	if errors.Is(err, garage.ErrInvalidPhoto) {
		fail(w, 422, garage.ErrInvalidPhoto.Error())
		return
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, http.StatusOK, v)
}

func (s *Server) deleteVehiclePhoto(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.DeleteVehiclePhoto(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, http.StatusOK, v)
}
