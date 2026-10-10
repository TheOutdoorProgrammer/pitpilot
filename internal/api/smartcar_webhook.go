package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/smartcar"
)

func (s *Server) smartcarWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.smartcar == nil || !s.smartcar.WebhooksConfigured() {
		fail(w, 503, "Smartcar webhook is not configured")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		fail(w, 413, "webhook exceeds request limit")
		return
	}
	if len(r.Header.Values("SC-Signature")) > 1 {
		fail(w, 401, "invalid webhook signature")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := s.smartcar.ReceiveWebhook(ctx, raw, r.Header.Get("SC-Signature"))
	if err != nil {
		switch {
		case errors.Is(err, smartcar.ErrWebhookSignature):
			fail(w, 401, "invalid webhook signature")
		case errors.Is(err, smartcar.ErrInvalid):
			fail(w, 400, "invalid webhook event")
		default:
			s.logger.ErrorContext(ctx, "smartcar webhook persistence failed")
			fail(w, 503, "webhook could not be stored")
		}
		return
	}
	respond(w, 200, result)
}
