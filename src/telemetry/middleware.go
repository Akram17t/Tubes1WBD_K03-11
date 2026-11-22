package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// RequestIDHeader dikirim balik ke client biar error di SPA bisa dicocokin dengan log.
const RequestIDHeader = "X-Request-ID"

// bikin request ID baru tiap request (format http-xxxx) dan taruh di context + header.
// Header X-Request-ID dari client sengaja diabaikan biar ga bisa dipalsuin
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := newRequestID()
		c.Request = c.Request.WithContext(WithRequestID(c.Request.Context(), id))
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// bikin span root buat tiap request HTTP, status error kalau response 5xx
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		route := routePattern(c)
		ctx, span := Tracer().Start(c.Request.Context(), c.Request.Method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", c.Request.Method),
				attribute.String("http.route", route),
				attribute.String("request_id", RequestID(c.Request.Context())),
			))
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		status := c.Writer.Status()
		span.SetAttributes(attribute.Int("http.response.status_code", status))
		if actor := ActorID(c.Request.Context()); actor != "" {
			span.SetAttributes(attribute.String("actor_id", actor))
		}
		if status >= 500 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", status))
		}
		span.End()
	}
}

// satu baris log JSON per request setelah selesai. Yang dicatat route pattern,
// bukan URL asli, biar query string (bisa berisi data) ga ikut ke log
func AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()

		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(c.Request.Context(), level, "http.request",
			"method", c.Request.Method,
			"route", routePattern(c),
			"status", status,
			"duration_ms", float64(time.Since(started).Microseconds())/1000,
		)
	}
}

// ID acak 8 byte hex, cukup buat korelasi dan ga ketebak
func newRequestID() string {
	buf := make([]byte, 8)
	rand.Read(buf)
	return "http-" + hex.EncodeToString(buf)
}

// route pattern dari gin (misal /api/admin/users/:user_id), "<unmatched>" kalau 404
func routePattern(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "<unmatched>"
}
