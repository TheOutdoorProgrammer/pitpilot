package smartcar

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var ErrWebhookSignature = errors.New("invalid Smartcar webhook signature")

type WebhookResult struct {
	Challenge string `json:"challenge,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (s *Service) WebhooksConfigured() bool { return s.managementToken != "" && s.webhookID != "" }

func (s *Service) ReceiveWebhook(ctx context.Context, raw []byte, signature string) (result WebhookResult, err error) {
	ctx, span := otel.Tracer("pitpilot/smartcar").Start(ctx, "smartcar.webhook")
	defer span.End()
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "webhook rejected")
		}
	}()
	if !s.WebhooksConfigured() {
		return result, ErrInvalid
	}
	validSignature := VerifyWebhookSignature(raw, signature, s.managementToken)
	// VERIFY is the provider's unsigned ownership challenge; it can never ingest data.
	event, err := DecodeWebhookPayload(raw)
	if err != nil {
		return result, ErrInvalid
	}
	if event.EventType != "VERIFY" && !validSignature {
		return result, ErrWebhookSignature
	}
	if event.WebhookID != s.webhookID {
		return result, ErrInvalid
	}
	span.SetAttributes(attribute.String("smartcar.event_type", event.EventType))
	if event.EventType == "VERIFY" {
		challenge, err := WebhookChallenge(event.Challenge, s.managementToken)
		if err != nil {
			return result, ErrInvalid
		}
		s.logger.InfoContext(ctx, "smartcar webhook verified")
		return WebhookResult{Challenge: challenge}, nil
	}
	if event.Mode != s.mode {
		return result, ErrInvalid
	}
	connection, err := s.store.SmartcarConnectionByRemoteKey(ctx, s.remoteKey(event.VehicleID))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetAttributes(attribute.String("smartcar.webhook_result", "unbound"))
		return WebhookResult{Status: "ignored"}, nil
	}
	if err != nil {
		return result, err
	}
	identity, err := s.identity(connection)
	if err != nil {
		return result, err
	}
	if identity.UserID != event.UserID || identity.VehicleID != event.VehicleID {
		return result, ErrInvalid
	}
	now := time.Now().UTC()
	adapted := adapt(event.VehicleID, event.Signals, now, connection.Status.AuthorizationStartedAt)
	activeErrors := 0
	for _, e := range event.Errors {
		if e.State == "ERROR" {
			activeErrors++
		}
	}
	update := garage.SmartcarWebhookUpdate{EventKey: fingerprint(event.WebhookID, event.EventID), ContentHash: event.ContentHash, EventType: event.EventType, ReceivedAt: now, Errors: activeErrors, Batch: adapted.Batch, Latest: adapted.Latest, Metrics: adapted.Metrics}
	duplicate, err := s.store.ApplySmartcarWebhook(ctx, connection, update)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookResult{Status: "ignored"}, nil
	}
	if err != nil {
		return result, err
	}
	span.SetAttributes(attribute.Int("smartcar.observations", len(adapted.Batch.Observations)), attribute.Int("smartcar.locations", len(adapted.Batch.Contexts)), attribute.Int("smartcar.webhook_errors", activeErrors), attribute.Bool("smartcar.duplicate", duplicate))
	s.logger.InfoContext(ctx, "smartcar webhook stored", "event_type", event.EventType, "observations", len(adapted.Batch.Observations), "locations", len(adapted.Batch.Contexts), "unavailable_signals", adapted.Unavailable, "active_errors", activeErrors, "duplicate", duplicate)
	return WebhookResult{Status: "received"}, nil
}
