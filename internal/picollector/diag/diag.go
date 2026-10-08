// Package diag keeps transport failures useful without exposing device replies or URLs.
package diag

import (
	"context"
	"errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"log/slog"
)

type failure struct {
	kind  string
	cause error
}

func (e *failure) Error() string              { return e.kind }
func (e *failure) Unwrap() error              { return e.cause }
func NewError(kind string, cause error) error { return &failure{kind, cause} }
func ErrorType(err error) string {
	var e *failure
	if errors.As(err, &e) {
		return e.kind
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "failed"
}
func Operation(ctx context.Context, name string, fn func(context.Context) error) error {
	ctx, span := otel.Tracer("pitpilot/picollector").Start(ctx, name)
	defer span.End()
	err := fn(ctx)
	if err != nil {
		kind := ErrorType(err)
		span.SetAttributes(attribute.String("error.type", kind))
		span.SetStatus(codes.Error, kind)
		slog.WarnContext(ctx, "collector operation failed", "operation", name, "error.type", kind)
	} else {
		slog.DebugContext(ctx, "collector operation completed", "operation", name)
	}
	return err
}
