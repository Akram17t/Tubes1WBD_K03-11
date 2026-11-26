package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// StartActivity membuat span dan pencatat hasil operasi service.
// Field tambahan hanya berisi ID atau penanda aman, bukan teks input atau pesan error mentah.
func StartActivity(ctx context.Context, event string, attrs ...attribute.KeyValue) (context.Context, func(error, ...attribute.KeyValue)) {
	ctx, span := Tracer().Start(ctx, event)
	span.SetAttributes(attrs...)
	if actorID := ActorID(ctx); actorID != "" {
		span.SetAttributes(attribute.String("actor_id", actorID))
	}
	if requestID := RequestID(ctx); requestID != "" {
		span.SetAttributes(attribute.String("request_id", requestID))
	}
	started := time.Now()
	return ctx, func(err error, resultAttrs ...attribute.KeyValue) {
		status, level := "success", slog.LevelInfo
		if err != nil {
			status, level = "error", slog.LevelWarn
			// Error driver atau validator bisa memuat credential, literal SQL, atau input pengguna.
			span.SetStatus(codes.Error, "Operation failed")
		} else {
			span.SetStatus(codes.Ok, "")
		}
		fields := append([]attribute.KeyValue{}, attrs...)
		fields = append(fields, resultAttrs...)
		fields = append(fields, attribute.String("status", status),
			attribute.Float64("duration_ms", float64(time.Since(started))/float64(time.Millisecond)))
		if state := SQLState(err); state != "" {
			fields = append(fields, attribute.String("sqlstate", state))
		}
		span.SetAttributes(fields...)
		logAttrs := make([]slog.Attr, 0, len(fields))
		for _, field := range fields {
			logAttrs = append(logAttrs, slog.Any(string(field.Key), field.Value.AsInterface()))
		}
		slog.LogAttrs(ctx, level, event, logAttrs...)
		span.End()
	}
}
