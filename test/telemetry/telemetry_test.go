package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"tubes-wbd/src/telemetry"
)

// pasang logger ke buffer selama test, balikin fungsi buat baca baris log
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(telemetry.NewLogger(&buf, slog.LevelDebug))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return func() []map[string]any {
		var lines []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("log bukan JSON: %v (%s)", err, line)
			}
			lines = append(lines, entry)
		}
		return lines
	}
}

// aktifin tracer console ke buffer biar span punya trace_id valid
func setupTracing(t *testing.T) *bytes.Buffer {
	t.Helper()
	var spans bytes.Buffer
	shutdown, err := telemetry.SetupTracing("test", telemetry.ExporterConsole, &spans)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdown(context.Background()) })
	return &spans
}

func TestLoggerAddsCorrelationAndRedacts(t *testing.T) {
	setupTracing(t)
	logs := captureLogs(t)
	ctx := telemetry.WithActor(telemetry.WithRequestID(context.Background(), "http-abc"), 7)
	ctx, span := telemetry.Tracer().Start(ctx, "test")
	slog.InfoContext(ctx, "auth.login", "password", "rahasia", "session_token", "tok", "Authorization", "Bearer x", "database_id", "3")
	span.End()

	entry := logs()[0]
	if entry["event"] != "auth.login" || entry["msg"] != nil {
		t.Errorf("msg harus diganti jadi event: %v", entry)
	}
	if entry["request_id"] != "http-abc" || entry["actor_id"] != "7" || entry["database_id"] != "3" {
		t.Errorf("field korelasi hilang: %v", entry)
	}
	if entry["trace_id"] != span.SpanContext().TraceID().String() || entry["span_id"] != span.SpanContext().SpanID().String() {
		t.Errorf("trace_id/span_id tidak cocok dengan span: %v", entry)
	}
	for _, key := range []string{"password", "session_token", "Authorization"} {
		if entry[key] != "[REDACTED]" {
			t.Errorf("%s harus disamarkan, dapat %v", key, entry[key])
		}
	}
}

func TestParseLevel(t *testing.T) {
	for _, raw := range []string{"debug", "INFO", "warn", "error"} {
		if _, err := telemetry.ParseLevel(raw); err != nil {
			t.Errorf("%s harus valid: %v", raw, err)
		}
	}
	if _, err := telemetry.ParseLevel("berisik"); err == nil {
		t.Error("level ngaco harus ditolak")
	}
}

func TestSetupTracingRejectsUnknownExporter(t *testing.T) {
	if _, err := telemetry.SetupTracing("test", "jaeger", &bytes.Buffer{}); err == nil {
		t.Fatal("exporter tidak dikenal harus error")
	}
}

func TestCarrierRoundTripKeepsTrace(t *testing.T) {
	setupTracing(t)
	ctx, span := telemetry.Tracer().Start(context.Background(), "api.enqueue")
	carrier := telemetry.InjectCarrier(ctx)
	span.End()
	if !strings.HasPrefix(carrier["traceparent"], "00-") || len(carrier) != 1 {
		t.Fatalf("carrier harus cuma berisi traceparent: %v", carrier)
	}

	workerCtx, workerSpan := telemetry.Tracer().Start(telemetry.ExtractCarrier(context.Background(), carrier), "worker.execute")
	defer workerSpan.End()
	if workerSpan.SpanContext().TraceID() != span.SpanContext().TraceID() {
		t.Error("span worker harus ada di trace yang sama dengan API")
	}
	_ = workerCtx
}

func TestHTTPMiddlewareLogsAndTraces(t *testing.T) {
	spans := setupTracing(t)
	logs := captureLogs(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(telemetry.RequestIDMiddleware(), telemetry.TraceMiddleware(), telemetry.AccessLogMiddleware())
	r.GET("/api/users/:user_id", func(c *gin.Context) {
		c.Request = c.Request.WithContext(telemetry.WithActor(c.Request.Context(), 5))
		c.Status(503)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/users/42?secret=xyz", nil))
	entry := logs()[0]
	if entry["event"] != "http.request" || entry["route"] != "/api/users/:user_id" || entry["status"] != float64(503) || entry["level"] != "ERROR" {
		t.Errorf("access log salah: %v", entry)
	}
	if entry["request_id"] != w.Header().Get(telemetry.RequestIDHeader) || entry["actor_id"] != "5" || entry["trace_id"] == nil {
		t.Errorf("access log harus punya request_id, actor_id, trace_id: %v", entry)
	}
	if strings.Contains(spans.String()+toJSON(entry), "xyz") {
		t.Error("query string tidak boleh masuk log/span")
	}
}

func TestLogSQLMasksParamsAndAddsSQLState(t *testing.T) {
	logs := captureLogs(t)
	err := &pgconn.PgError{Code: "23505", Message: "duplicate key value", Detail: "Key (email)=(rahasia@x.id) already exists."}
	telemetry.LogSQL(context.Background(), telemetry.SQLEvent{
		Event: "sql.query", Query: "INSERT INTO users (email) VALUES ($1)", Args: []any{"rahasia@x.id", int64(3)},
		Started: time.Now(), Err: err, Attrs: []any{"database_id", "1"},
	})
	telemetry.LogSQL(context.Background(), telemetry.SQLEvent{Event: "sql.query", Query: "SELECT 1", Started: time.Now(), Rows: 0, RowsKnown: true})

	lines := logs()
	failed, ok := lines[0], lines[1]
	if failed["sqlstate"] != "23505" || failed["status"] != "error" || failed["level"] != "ERROR" || failed["database_id"] != "1" {
		t.Errorf("log SQL gagal salah: %v", failed)
	}
	if strings.Contains(toJSON(failed), "rahasia") {
		t.Errorf("nilai parameter / detail error bocor: %v", failed)
	}
	if params := failed["params"].([]any); params[0] != "$1:string" || params[1] != "$2:int64" {
		t.Errorf("parameter harus tersamar jadi tipe: %v", params)
	}
	if _, hasRows := failed["rows"]; hasRows {
		t.Error("rows tidak boleh ditulis kalau tidak diketahui")
	}
	if ok["rows"] != float64(0) || ok["status"] != "ok" {
		t.Errorf("log SQL sukses salah: %v", ok)
	}
	if telemetry.SafeError(errors.New("biasa")) != "biasa" {
		t.Error("error non-postgres harus apa adanya")
	}
}

// ubah map jadi string JSON buat dicek isinya
func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
