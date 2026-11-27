package telemetry

import (
	"context"
	"fmt"
	"io"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Pilihan TRACE_EXPORTER yang didukung.
const (
	ExporterConsole = "console"
	ExporterNone    = "none"
	ExporterOTLP    = "otlp"
)

const tracerName = "tubes-wbd"

// pasang tracer global + propagator W3C. "console" nulis span JSON ke w, "otlp" kirim ke Collector
// (alamat dari OTEL_EXPORTER_OTLP_ENDPOINT), "none" matiin export. Panggil shutdown biar span ke-flush
func SetupTracing(serviceName, exporter string, w io.Writer) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	var exp sdktrace.SpanExporter
	var err error
	switch exporter {
	case ExporterNone:
		return func(context.Context) error { return nil }, nil
	case ExporterConsole:
		exp, err = stdouttrace.New(stdouttrace.WithWriter(w))
	case ExporterOTLP:
		exp, err = otlptracehttp.New(context.Background())
	default:
		return nil, fmt.Errorf("TRACE_EXPORTER must be %s, %s, or %s", ExporterConsole, ExporterOTLP, ExporterNone)
	}
	if err != nil {
		return nil, fmt.Errorf("create %s trace exporter: %w", exporter, err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(serviceResource(serviceName)),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

// resource bersama trace & log, service.name dipakai Grafana buat nyambungin keduanya
func serviceResource(serviceName string) *resource.Resource {
	return resource.NewSchemaless(attribute.String("service.name", serviceName))
}

// tracer bersama aplikasi, dipakai semua modul buat bikin span
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// tutup span sambil nyatet error (kalau ada), biar status span sesuai hasilnya
func EndSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// ambil trace context jadi map (isinya traceparent) buat disimpan di job queue.
// Isinya cuma ID trace, bukan cookie/credential
func InjectCarrier(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier
}

// kebalikan InjectCarrier, dipakai worker biar span-nya nyambung ke trace API
func ExtractCarrier(ctx context.Context, carrier map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(carrier))
}
