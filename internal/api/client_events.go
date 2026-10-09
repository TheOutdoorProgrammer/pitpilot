package api

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var clientOperations = map[string]bool{
	"vehicles.list": true, "vehicle.get": true, "vehicle.create": true, "vehicle.update": true, "vehicle.delete": true,
	"vehicle.photo.get": true, "vehicle.photo.put": true, "vehicle.photo.delete": true,
	"records.list": true, "record.create": true, "record.update": true, "record.delete": true,
	"reminders.list": true, "reminder.create": true, "reminder.update": true, "reminder.delete": true,
	"trips.list": true, "trip.create": true, "trip.delete": true, "export.get": true,
	"signals.latest": true, "signals.history": true, "signals.catalog": true,
	"devices.list": true, "device.create": true, "device.update": true, "device.revoke": true,
	"smartcar.configuration": true, "smartcar.status": true, "smartcar.session.create": true,
	"smartcar.session.complete": true, "smartcar.session.bind": true, "smartcar.sync": true, "smartcar.disconnect": true,
}

var clientFailureKinds = map[string]bool{
	"dns": true, "timeout": true, "connection": true,
	"tls": true, "offline": true, "other": true,
}

func (s *Server) clientEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var event struct {
		Operation   string `json:"operation"`
		DurationMS  int64  `json:"durationMs"`
		StatusCode  int    `json:"statusCode"`
		FailureKind string `json:"failureKind,omitempty"`
	}
	if !decode(w, r, &event) {
		return
	}
	if !clientOperations[event.Operation] || event.DurationMS < 0 || event.DurationMS > 120000 || (event.StatusCode != 0 && (event.StatusCode < 100 || event.StatusCode > 599)) {
		fail(w, 422, "invalid client event")
		return
	}
	if event.FailureKind != "" && (!clientFailureKinds[event.FailureKind] || event.StatusCode != 0) {
		fail(w, 422, "invalid client event")
		return
	}
	ended := time.Now()
	ctx, span := otel.Tracer("pitpilot/ios").Start(r.Context(), "ios."+event.Operation, trace.WithTimestamp(ended.Add(-time.Duration(event.DurationMS)*time.Millisecond)), trace.WithAttributes(attribute.String("client.platform", "ios")))
	if event.StatusCode != 0 {
		span.SetAttributes(attribute.Int("http.response.status_code", event.StatusCode))
	}
	fields := []any{"operation", event.Operation, "status", event.StatusCode, "duration_ms", event.DurationMS}
	if event.FailureKind != "" {
		span.SetAttributes(attribute.String("error.type", event.FailureKind))
		fields = append(fields, "failure_kind", event.FailureKind)
	}
	if event.StatusCode == 0 || event.StatusCode >= 400 {
		span.SetStatus(codes.Error, "client request failed")
		s.logger.WarnContext(ctx, "client request failed", fields...)
	} else {
		s.logger.InfoContext(ctx, "client request completed", fields...)
	}
	span.End(trace.WithTimestamp(ended))
	w.WriteHeader(http.StatusNoContent)
}
