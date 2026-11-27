// Package telemetry berisi fondasi structured log (slog JSON) dan tracing OpenTelemetry.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type contextKey string

const (
	requestIDKey contextKey = "telemetry.request_id"
	actorIDKey   contextKey = "telemetry.actor_id"
	redacted                = "[REDACTED]"
)

// potongan nama key yang nilainya wajib disamarkan di log
var sensitiveKeyParts = []string{"password", "secret", "token", "cookie", "authorization", "session", "dsn", "encryption_key"}

// bikin logger JSON yang otomatis nambah request_id, actor_id, trace_id, span_id dari context.
// Field "msg" bawaan slog diganti jadi "event". extra (misal handler OTLP) ikut nerima record yang sama
func NewLogger(w io.Writer, level slog.Level, extra ...slog.Handler) *slog.Logger {
	var base slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: replaceAttr})
	if len(extra) > 0 {
		base = slog.NewMultiHandler(append([]slog.Handler{base}, extra...)...)
	}
	return slog.New(contextHandler{Handler: base})
}

// ubah teks LOG_LEVEL (debug/info/warn/error) jadi slog.Level
func ParseLevel(raw string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.TrimSpace(raw))); err != nil {
		return 0, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
	return level, nil
}

// titip request correlation ID ke context, dibaca logger dan response meta
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// ambil request ID dari context, string kosong kalau belum ada
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// titip ID user yang lagi login, biar semua log request itu punya actor_id
func WithActor(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, actorIDKey, strconv.FormatInt(userID, 10))
}

// ambil actor_id dari context, string kosong kalau anonim
func ActorID(ctx context.Context) string {
	id, _ := ctx.Value(actorIDKey).(string)
	return id
}

// contextHandler membungkus JSON handler buat nyisipin ID korelasi dari context.
type contextHandler struct {
	slog.Handler
}

// tambah field korelasi dari context sebelum record ditulis
func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if ctx != nil {
		if id := RequestID(ctx); id != "" {
			record.AddAttrs(slog.String("request_id", id))
		}
		if id := ActorID(ctx); id != "" {
			record.AddAttrs(slog.String("actor_id", id))
		}
		if span := trace.SpanContextFromContext(ctx); span.IsValid() {
			record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
		}
	}
	return h.Handler.Handle(ctx, record)
}

// wajib di-override biar logger.With(...) tetap lewat contextHandler
func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// sama kayak WithAttrs, buat logger.WithGroup(...)
func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}

// rename msg jadi event, dan samarkan value yang key-nya kelihatan sensitif
func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.MessageKey {
		attr.Key = "event"
		return attr
	}
	if IsSensitiveKey(attr.Key) {
		return slog.String(attr.Key, redacted)
	}
	return attr
}

// true kalau nama key mengandung kata kayak password/token/cookie/session
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}
