package telemetry_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"tubes-wbd/src/telemetry"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestActivityRecordsOutcomeWithoutRawErrors memeriksa korelasi log/span dan penyamaran error.
func TestActivityRecordsOutcomeWithoutRawErrors(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				provider.Shutdown(context.Background())
				otel.SetTracerProvider(previous)
			})
			logs := captureLogs(t)
			ctx := telemetry.WithActor(telemetry.WithRequestID(context.Background(), "http-activity"), 7)
			ctx, parent := telemetry.Tracer().Start(ctx, "api")
			defer parent.End()
			_, finish := telemetry.StartActivity(ctx, "admin.group.update", attribute.Int64("group_id", 3))
			var err error
			status, spanCode := "success", codes.Ok
			if failed {
				err = &pgconn.PgError{Code: "23505", Message: "private-password", Detail: "private-sql-literal"}
				status, spanCode = "error", codes.Error
			}
			finish(err)
			lines := logs()
			ended := recorder.Ended()
			if len(lines) != 1 || len(ended) != 1 {
				t.Fatalf("harus satu hasil log dan span: logs=%v spans=%d", lines, len(ended))
			}
			entry, span := lines[0], ended[0]
			if entry["event"] != "admin.group.update" || entry["status"] != status || entry["actor_id"] != "7" ||
				entry["request_id"] != "http-activity" || entry["group_id"] != float64(3) || entry["duration_ms"] == nil ||
				entry["trace_id"] != parent.SpanContext().TraceID().String() || entry["span_id"] != span.SpanContext().SpanID().String() {
				t.Fatalf("korelasi/hasil log salah: %v", entry)
			}
			if span.Parent().SpanID() != parent.SpanContext().SpanID() || span.Status().Code != spanCode {
				t.Fatalf("span tidak mengikuti parent/hasil operasi: %+v", span.Status())
			}
			if failed && entry["sqlstate"] != "23505" {
				t.Fatalf("SQLSTATE aman harus tetap tersedia: %v", entry)
			}
			if strings.Contains(toJSON(entry)+fmt.Sprint(span.Attributes(), span.Events(), span.Status()), "private-") {
				t.Fatal("pesan/detail error mentah bocor ke log atau span")
			}
		})
	}
}
