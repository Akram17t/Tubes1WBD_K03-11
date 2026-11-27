package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// bikin handler slog yang ngirim log ke Collector lewat OTLP (alamat dari OTEL_EXPORTER_OTLP_ENDPOINT).
// "none" balikin nil, jadi log cuma ke stdout kayak biasa
func SetupLogs(serviceName, exporter string, level slog.Level) (slog.Handler, func(context.Context) error, error) {
	if exporter != ExporterOTLP {
		return nil, func(context.Context) error { return nil }, nil
	}
	exp, err := otlploghttp.New(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("create otlp log exporter: %w", err)
	}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(serviceResource(serviceName)),
	)
	handler := otelslog.NewHandler(tracerName, otelslog.WithLoggerProvider(provider))
	return RedactHandler(handler, level), provider.Shutdown, nil
}

// bungkus handler apa pun biar dapat filter LOG_LEVEL + masking yang sama kayak stdout
func RedactHandler(h slog.Handler, level slog.Level) slog.Handler {
	return redactHandler{Handler: h, level: level}
}

// redactHandler nerapin level + masking yang sama kayak log JSON stdout sebelum dikirim keluar
type redactHandler struct {
	slog.Handler
	level slog.Level
}

// log di bawah LOG_LEVEL ga ikut dikirim ke Collector
func (h redactHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// salin record sambil nyamarin attr sensitif (password, token, cookie, dst)
func (h redactHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactAttr(attr))
		return true
	})
	return h.Handler.Handle(ctx, clean)
}

// attr dari logger.With(...) juga harus lewat masking
func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		clean[i] = redactAttr(attr)
	}
	return redactHandler{Handler: h.Handler.WithAttrs(clean), level: h.level}
}

// group ikut dibungkus biar masking tetap jalan di dalamnya
func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}

// samarkan value kalau key-nya sensitif, termasuk attr di dalam group
func redactAttr(attr slog.Attr) slog.Attr {
	if IsSensitiveKey(attr.Key) {
		return slog.String(attr.Key, redacted)
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		clean := make([]any, len(group))
		for i, inner := range group {
			clean[i] = redactAttr(inner)
		}
		return slog.Group(attr.Key, clean...)
	}
	return attr
}
