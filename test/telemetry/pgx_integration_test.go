package telemetry_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"tubes-wbd/src/telemetry"
)

// query lewat koneksi yang dipasangi QueryTracer otomatis tercatat: query, parameter tersamar,
// rows, trace_id dari request, dan SQLSTATE kalau gagal
func TestQueryTracerLogsEveryQuery(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL kosong, integration test dilewati")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.Contains(strings.ToLower(config.Database), "test") {
		t.Fatal("TEST_DATABASE_URL harus menunjuk database khusus pengujian")
	}
	config.Tracer = telemetry.QueryTracer{Database: "platform"}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close() })

	setupTracing(t)
	logs := captureLogs(t)
	ctx := telemetry.WithRequestID(context.Background(), "http-sql")
	ctx, span := telemetry.Tracer().Start(ctx, "GET /api/test")

	var value string
	if err := db.QueryRowContext(ctx, `
		SELECT $1::text
	`, "nilai-rahasia").Scan(&value); err != nil {
		t.Fatal(err)
	}
	_, failErr := db.ExecContext(ctx, `SELECT * FROM tabel_yang_tidak_ada`)
	span.End()
	if failErr == nil {
		t.Fatal("query ke tabel yang tidak ada harusnya error")
	}

	var ok, failed map[string]any
	for _, entry := range logs() {
		if entry["event"] != "sql.query" {
			continue
		}
		if strings.Contains(entry["query"].(string), "$1::text") {
			ok = entry
		}
		if strings.Contains(entry["query"].(string), "tabel_yang_tidak_ada") {
			failed = entry
		}
	}
	if ok == nil || failed == nil {
		t.Fatalf("query sukses dan gagal harus tercatat otomatis: %v", logs())
	}
	if ok["db"] != "platform" || ok["rows"] != float64(1) || ok["request_id"] != "http-sql" || ok["status"] != "ok" {
		t.Errorf("log query sukses salah: %v", ok)
	}
	if ok["trace_id"] != span.SpanContext().TraceID().String() || ok["span_id"] == span.SpanContext().SpanID().String() {
		t.Errorf("query harus punya span sendiri di dalam trace request: %v", ok)
	}
	if params, _ := ok["params"].([]any); len(params) != 1 || params[0] != "$1:string" || ok["query"] != "SELECT $1::text" {
		t.Errorf("argumen internal pgx harus dibuang dan query dirapihin: %v %v", ok["params"], ok["query"])
	}
	if strings.Contains(toJSON(ok), "nilai-rahasia") {
		t.Error("nilai parameter tidak boleh masuk log")
	}
	if failed["sqlstate"] != "42P01" || failed["status"] != "error" || failed["level"] != "ERROR" {
		t.Errorf("log query gagal harus punya SQLSTATE: %v", failed)
	}
}
