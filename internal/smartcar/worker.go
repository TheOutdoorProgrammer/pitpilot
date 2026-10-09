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

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil {
			v, err := s.store.ClaimSmartcar(ctx, time.Now(), s.interval)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				s.logger.ErrorContext(ctx, "smartcar scheduler failed")
				break
			}
			s.process(ctx, v)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *Service) process(parent context.Context, v garage.SmartcarConnection) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	ctx, span := otel.Tracer("pitpilot/smartcar").Start(ctx, "smartcar.sync")
	defer span.End()
	now := time.Now().UTC()
	previousSuccess, previousObservation := v.Status.LastSuccessAt, v.Status.LatestObservedAt
	v.Status.LastAttemptAt = &now
	i, err := s.identity(v)
	var result adapted
	if err == nil {
		var signals []remoteSignal
		signals, err = s.client.signals(ctx, i.VehicleID, i.UserID)
		if err == nil {
			result = adapt(i.VehicleID, signals, now, v.Status.AuthorizationStartedAt)
		}
	}
	var batch *garage.SignalBatch
	var appBackoff time.Time
	if err != nil {
		v.Failures++
		code := "temporarily_unavailable"
		var p *providerError
		if errors.As(err, &p) {
			code = p.Code
		}
		v.Status.ErrorCode = code
		v.Status.State = "temporary_error"
		if code == "provisioning" && v.Status.LastSuccessAt == nil {
			v.Status.State = "provisioning"
		}
		if code == "reconnect_required" {
			v.Status.State = "reconnect_required"
		}
		delay := max(s.interval, time.Minute*time.Duration(1<<min(v.Failures, 6)))
		v.RetryAt = now.Add(delay)
		if p != nil && !p.RetryAt.IsZero() && p.RetryAt.After(v.RetryAt) {
			v.RetryAt = p.RetryAt
		}
		v.NextAttemptAt = v.RetryAt
		if p != nil && (p.AppWide || p.Code == "application_authentication") {
			appBackoff = v.RetryAt
		}
		span.SetStatus(codes.Error, "provider synchronization failed")
	} else {
		v.Failures = 0
		v.Status.State = "connected"
		v.Status.ErrorCode = ""
		if result.Latest != nil {
			v.Status.LastSuccessAt = &now
		} else if previousObservation == nil {
			v.Status.LastSuccessAt = nil
		}
		v.Status.SupportedMetrics = result.Metrics
		v.Status.UnavailableSignals = result.Unavailable
		v.Status.UnsupportedSignals = result.Unsupported
		if result.Latest != nil && (v.Status.LatestObservedAt == nil || result.Latest.After(*v.Status.LatestObservedAt)) {
			v.Status.LatestObservedAt = result.Latest
		}
		if result.Reconnect {
			v.Status.State = "reconnect_required"
			v.Status.ErrorCode = "reconnect_required"
			span.SetStatus(codes.Error, "provider account requires attention")
		} else if result.Latest == nil {
			v.Status.State = "provisioning"
			if previousObservation != nil {
				v.Status.State = "temporary_error"
			}
			v.Status.ErrorCode = "no_timestamped_signals"
		}
		v.NextAttemptAt = now.Add(s.interval)
		v.RetryAt = time.Time{}
		batch = &result.Batch
	}
	v.Status.NextAttemptAt = &v.NextAttemptAt
	if finishErr := s.store.FinishSmartcar(ctx, v, batch, appBackoff); finishErr != nil {
		if errors.Is(finishErr, garage.ErrSignalConflict) {
			v.Status.LastSuccessAt, v.Status.LatestObservedAt = previousSuccess, previousObservation
			v.Status.State = "temporary_error"
			v.Status.ErrorCode = "observation_conflict"
			v.RetryAt = now.Add(s.interval)
			v.NextAttemptAt = v.RetryAt
			v.Status.NextAttemptAt = &v.NextAttemptAt
			finishErr = s.store.FinishSmartcar(ctx, v, nil, time.Time{})
		}
		span.SetStatus(codes.Error, "synchronization persistence failed")
		if finishErr != nil && !errors.Is(finishErr, sql.ErrNoRows) && !errors.Is(finishErr, garage.ErrSmartcarConflict) {
			s.logger.ErrorContext(ctx, "smartcar synchronization persistence failed")
		}
	}
	span.SetAttributes(attribute.String("smartcar.state", v.Status.State), attribute.Int("smartcar.observations", len(result.Batch.Observations)), attribute.Int("smartcar.unavailable_signals", result.Unavailable))
	s.logger.InfoContext(ctx, "smartcar synchronization completed", "state", v.Status.State, "observations", len(result.Batch.Observations), "unavailable_signals", result.Unavailable)
}
