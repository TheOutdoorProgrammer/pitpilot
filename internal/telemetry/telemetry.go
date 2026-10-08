package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func Start(ctx context.Context, version string) (*slog.Logger, func(context.Context) error, error) {
	return StartTo(ctx, version, os.Stdout)
}

func StartTo(ctx context.Context, version string, output io.Writer) (*slog.Logger, func(context.Context) error, error) {
	console := slog.NewJSONHandler(output, nil)
	logger := slog.New(correlated{handlers: []slog.Handler{console}})
	shutdown := func(context.Context) error { return nil }
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" || os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return logger, shutdown, nil
	}
	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK(), resource.WithAttributes(attribute.String("service.name", "pitpilot"), attribute.String("service.version", version)))
	if err != nil {
		return nil, nil, err
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	otel.SetTracerProvider(tp)
	le, err := otlploghttp.New(ctx)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, nil, err
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(le)))
	logger = slog.New(correlated{handlers: []slog.Handler{console, otelslog.NewHandler("pitpilot", otelslog.WithLoggerProvider(lp))}})
	// Exporter errors can contain endpoints and headers; keep feedback local and bounded.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { slog.New(console).Error("telemetry export failed") }))
	return logger, func(ctx context.Context) error { return errors.Join(lp.Shutdown(ctx), tp.Shutdown(ctx)) }, nil
}

type correlated struct{ handlers []slog.Handler }

func (h correlated) Enabled(ctx context.Context, l slog.Level) bool {
	for _, v := range h.handlers {
		if v.Enabled(ctx, l) {
			return true
		}
	}
	return false
}
func (h correlated) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	var errs []error
	for _, v := range h.handlers {
		if v.Enabled(ctx, r.Level) {
			errs = append(errs, v.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}
func (h correlated) WithAttrs(a []slog.Attr) slog.Handler {
	out := correlated{}
	for _, v := range h.handlers {
		out.handlers = append(out.handlers, v.WithAttrs(a))
	}
	return out
}
func (h correlated) WithGroup(n string) slog.Handler {
	out := correlated{}
	for _, v := range h.handlers {
		out.handlers = append(out.handlers, v.WithGroup(n))
	}
	return out
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func HTTP(mux *http.ServeMux, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern == "GET /healthz" || pattern == "GET /readyz" {
			mux.ServeHTTP(w, r)
			return
		}
		if pattern == "" {
			pattern = "unmatched"
		}
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer("pitpilot/http").Start(ctx, pattern, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.route", pattern)))
		defer span.End()
		writer := &statusWriter{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if recover() != nil {
				span.SetStatus(codes.Error, "request panic")
				logger.ErrorContext(ctx, "request panic")
				if writer.status == 0 {
					http.Error(writer, "internal server error", 500)
				}
			}
			status := writer.status
			if status == 0 {
				status = 200
			}
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= 500 {
				span.SetStatus(codes.Error, "request failed")
			}
			logger.InfoContext(ctx, "request completed", "route", pattern, "status", status, "duration_ms", time.Since(started).Milliseconds())
		}()
		mux.ServeHTTP(writer, r.WithContext(ctx))
	})
}
