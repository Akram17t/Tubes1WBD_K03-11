package telemetry_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/trace"
	"tubes-wbd/src/telemetry"
)

// durasi yang dicatat di dalam span nempelin trace_id sebagai exemplar (bukti korelasi metrics -> trace)
func TestObserveDurationAddsTraceExemplar(t *testing.T) {
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "test_duration_seconds"})
	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	telemetry.ObserveDuration(ctx, histogram, 1500*time.Millisecond)

	var metric dto.Metric
	if err := histogram.Write(&metric); err != nil {
		t.Fatal(err)
	}
	if got := metric.GetHistogram().GetSampleSum(); got != 1.5 {
		t.Fatalf("sample sum = %v, want 1.5", got)
	}
	found := false
	for _, bucket := range metric.GetHistogram().GetBucket() {
		for _, label := range bucket.GetExemplar().GetLabel() {
			found = found || (label.GetName() == "trace_id" && label.GetValue() == traceID.String())
		}
	}
	if !found {
		t.Fatal("exemplar trace_id tidak ditemukan di histogram")
	}

	// tanpa span tetap tercatat, cuma tanpa exemplar
	telemetry.ObserveDuration(context.Background(), histogram, time.Second)
	if testutil.CollectAndCount(histogram) != 1 {
		t.Fatal("histogram harus tetap satu seri")
	}
}

// pool platform dan target keluar dengan label pool/database_id/state saja, tanpa koneksi beneran
func TestPoolCollectorLabels(t *testing.T) {
	platform := openUnconnected(t)
	target := openUnconnected(t)
	target.SetMaxOpenConns(7)
	collector := telemetry.NewPoolCollector(platform, func() map[int64]sql.DBStats {
		return map[int64]sql.DBStats{3: target.Stats()}
	})

	expected := `
# HELP db_pool_max_open_connections Batas koneksi terbuka pool.
# TYPE db_pool_max_open_connections gauge
db_pool_max_open_connections{database_id="",pool="platform"} 0
db_pool_max_open_connections{database_id="3",pool="target"} 7
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "db_pool_max_open_connections"); err != nil {
		t.Fatal(err)
	}
	// 2 pool x (2 state koneksi + max open + wait count + wait seconds)
	if got := testutil.CollectAndCount(collector); got != 10 {
		t.Fatalf("jumlah seri = %d, want 10", got)
	}
}

// metric worker cuma boleh pakai label bernilai terbatas, bukan job/user ID atau SQL
func TestMigrationMetricLabelsStayLowCardinality(t *testing.T) {
	allowed := map[string]bool{"operation": true, "status": true, "result": true, "direction": true}
	collectors := map[string]prometheus.Collector{
		"queue_wait": telemetry.QueueWait, "lock_wait": telemetry.LockWait, "duration": telemetry.ExecutionDuration,
		"attempts": telemetry.JobAttempts, "retries": telemetry.JobRetries,
		"batches": telemetry.BatchesCommitted, "rows": telemetry.RowsCommitted,
	}
	for name, collector := range collectors {
		descs := make(chan *prometheus.Desc, 4)
		collector.Describe(descs)
		close(descs)
		for desc := range descs {
			for _, label := range variableLabels(desc.String()) {
				if !allowed[label] {
					t.Errorf("%s memakai label %q yang tidak terbatas", name, label)
				}
			}
		}
	}
}

// log yang dikirim ke Collector tetap disamarkan dan ikut LOG_LEVEL
func TestRedactHandlerMasksAndFiltersLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(telemetry.RedactHandler(slog.NewJSONHandler(&buf, nil), slog.LevelInfo))
	logger.With("session_token", "abc").Info("auth.login", "password", "rahasia", "username", "editor",
		slog.Group("db", "dsn", "postgres://u:p@h/db"))
	logger.Debug("debug.dibuang")

	out := buf.String()
	for _, secret := range []string{"abc", "rahasia", "u:p@h"} {
		if strings.Contains(out, secret) {
			t.Fatalf("nilai sensitif %q bocor: %s", secret, out)
		}
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &entry); err != nil {
		t.Fatalf("harus tepat satu baris log (debug dibuang): %v\n%s", err, out)
	}
	if entry["username"] != "editor" {
		t.Fatalf("field biasa ikut hilang: %v", entry)
	}
}

// jumlah job per status dibaca dari migration_jobs; status yang kosong tetap keluar bernilai 0
func TestJobStatusCollectorCountsQueue(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL kosong, integration test dilewati")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.Contains(strings.ToLower(config.Database), "test") {
		t.Fatal("TEST_DATABASE_URL harus menunjuk database khusus pengujian")
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close() })

	var queued float64
	if err := db.QueryRow(`SELECT count(*) FROM migration_jobs WHERE status = 'queued'`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	collector := telemetry.NewJobStatusCollector(db)
	if got := testutil.CollectAndCount(collector); got != 8 {
		t.Fatalf("jumlah status = %d, want 8", got)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range families[0].GetMetric() {
		if metric.GetLabel()[0].GetValue() == "queued" && metric.GetGauge().GetValue() != queued {
			t.Fatalf("queued = %v, want %v", metric.GetGauge().GetValue(), queued)
		}
	}
}

// sql.DB tanpa dial: Stats() bisa dibaca tanpa server PostgreSQL
func openUnconnected(t *testing.T) *sql.DB {
	config, err := pgx.ParseConfig("postgres://u@127.0.0.1:1/x")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close() })
	return db
}

// ambil nama variableLabels dari Desc.String(), format: "... variableLabels: {a,b}}"
func variableLabels(desc string) []string {
	_, rest, ok := strings.Cut(desc, "variableLabels: {")
	if !ok {
		return nil
	}
	list, _, _ := strings.Cut(rest, "}")
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}
