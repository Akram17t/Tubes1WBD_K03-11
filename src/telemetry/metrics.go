package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

// Label sengaja cuma yang nilainya terbatas (operation, status, direction, pool, database_id).
// Job ID, user ID, dan SQL tidak pernah jadi label biar cardinality tetap kecil.
var (
	// bucket dari 10 ms sampai ~11 menit, cukup buat queue wait sampai migration besar
	durationBuckets = prometheus.ExponentialBuckets(0.01, 3, 12)

	QueueWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "migration_queue_wait_seconds", Help: "Waktu job menunggu di queue sampai diklaim worker.", Buckets: durationBuckets,
	}, []string{"operation"})
	LockWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "migration_lock_wait_seconds", Help: "Waktu worker menunggu advisory lock database.", Buckets: durationBuckets,
	}, []string{"result"})
	ExecutionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "migration_execution_duration_seconds", Help: "Durasi attempt migration dari lock didapat sampai hasil dicatat.", Buckets: durationBuckets,
	}, []string{"operation", "status"})
	JobAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "migration_job_attempts_total", Help: "Attempt job yang selesai per hasil.",
	}, []string{"operation", "status"})
	JobRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "migration_job_retries_total", Help: "Retry otomatis yang dijadwalkan worker.",
	}, []string{"operation"})
	BatchesCommitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "migration_batches_committed_total", Help: "Batch yang sudah di-commit ke database target.",
	}, []string{"direction"})
	RowsCommitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "migration_batch_rows_total", Help: "Row yang diproses batch yang sudah di-commit.",
	}, []string{"direction"})
)

// catat durasi ke histogram sambil nempelin trace_id sebagai exemplar, biar Grafana bisa lompat ke trace-nya
func ObserveDuration(ctx context.Context, observer prometheus.Observer, d time.Duration) {
	span := trace.SpanContextFromContext(ctx)
	if exemplar, ok := observer.(prometheus.ExemplarObserver); ok && span.IsValid() {
		exemplar.ObserveWithExemplar(d.Seconds(), prometheus.Labels{"trace_id": span.TraceID().String()})
		return
	}
	observer.Observe(d.Seconds())
}

// buka /metrics di listener sendiri (bukan port API) biar endpoint ini ga ikut kebuka ke publik.
// Format OpenMetrics dinyalakan karena exemplar cuma kekirim lewat format itu
func ServeMetrics(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	go func() {
		slog.Info("metrics.server_started", "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics.server_failed", "error", SafeError(err))
		}
	}()
}

// PoolSource ngasih snapshot statistik pool per database target (key = database_id).
type PoolSource func() map[int64]sql.DBStats

// collector pool koneksi: dibaca pas di-scrape dari sql.DB.Stats(), jadi ga perlu goroutine polling
type poolCollector struct {
	platform *sql.DB
	targets  PoolSource
}

var (
	poolConnections = prometheus.NewDesc("db_pool_connections", "Koneksi di pool per state (in_use/idle).", []string{"pool", "database_id", "state"}, nil)
	poolMaxOpen     = prometheus.NewDesc("db_pool_max_open_connections", "Batas koneksi terbuka pool.", []string{"pool", "database_id"}, nil)
	poolWaitCount   = prometheus.NewDesc("db_pool_wait_total", "Jumlah request yang harus menunggu koneksi kosong.", []string{"pool", "database_id"}, nil)
	poolWaitSeconds = prometheus.NewDesc("db_pool_wait_seconds_total", "Total waktu menunggu koneksi kosong.", []string{"pool", "database_id"}, nil)
)

// daftarin collector pool platform + semua pool target yang lagi kebuka
func RegisterPoolMetrics(platform *sql.DB, targets PoolSource) {
	prometheus.MustRegister(NewPoolCollector(platform, targets))
}

// collector pool tanpa registrasi global, dipakai juga oleh test
func NewPoolCollector(platform *sql.DB, targets PoolSource) prometheus.Collector {
	return poolCollector{platform: platform, targets: targets}
}

// daftar metric yang mungkin dikirim collector pool
func (c poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- poolConnections
	ch <- poolMaxOpen
	ch <- poolWaitCount
	ch <- poolWaitSeconds
}

// baca statistik pool saat Prometheus scrape
func (c poolCollector) Collect(ch chan<- prometheus.Metric) {
	collectPool(ch, "platform", "", c.platform.Stats())
	if c.targets == nil {
		return
	}
	for id, stats := range c.targets() {
		collectPool(ch, "target", strconv.FormatInt(id, 10), stats)
	}
}

// ubah satu sql.DBStats jadi deretan metric dengan label pool + database_id
func collectPool(ch chan<- prometheus.Metric, pool, databaseID string, s sql.DBStats) {
	ch <- prometheus.MustNewConstMetric(poolConnections, prometheus.GaugeValue, float64(s.InUse), pool, databaseID, "in_use")
	ch <- prometheus.MustNewConstMetric(poolConnections, prometheus.GaugeValue, float64(s.Idle), pool, databaseID, "idle")
	ch <- prometheus.MustNewConstMetric(poolMaxOpen, prometheus.GaugeValue, float64(s.MaxOpenConnections), pool, databaseID)
	ch <- prometheus.MustNewConstMetric(poolWaitCount, prometheus.CounterValue, float64(s.WaitCount), pool, databaseID)
	ch <- prometheus.MustNewConstMetric(poolWaitSeconds, prometheus.CounterValue, s.WaitDuration.Seconds(), pool, databaseID)
}

// collector jumlah job per status. Dihitung dari tabel queue tiap scrape, jadi angkanya sama
// walaupun worker-nya banyak. Cukup didaftarkan di satu proses (API) biar ga dobel
type jobStatusCollector struct{ db *sql.DB }

var jobsByStatus = prometheus.NewDesc("migration_jobs", "Jumlah job migration per status saat ini.", []string{"status"}, nil)

// semua status job didaftarkan supaya yang nol tetap muncul di dashboard
var jobStatuses = []string{"queued", "running", "success", "failed", "compensated", "partial", "partially_reverted", "outcome_unknown"}

// daftarin collector status job ke registry global
func RegisterJobStatusMetrics(db *sql.DB) {
	prometheus.MustRegister(NewJobStatusCollector(db))
}

// collector status job tanpa registrasi global, dipakai juga oleh test
func NewJobStatusCollector(db *sql.DB) prometheus.Collector {
	return jobStatusCollector{db: db}
}

// cuma satu jenis metric: migration_jobs{status}
func (c jobStatusCollector) Describe(ch chan<- *prometheus.Desc) { ch <- jobsByStatus }

// query jumlah job per status saat Prometheus scrape
func (c jobStatusCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	counts, err := countJobs(ctx, c.db)
	if err != nil {
		// scrape gagal ditandai lewat invalid metric, bukan angka nol yang menyesatkan
		ch <- prometheus.NewInvalidMetric(jobsByStatus, err)
		return
	}
	for _, status := range jobStatuses {
		ch <- prometheus.MustNewConstMetric(jobsByStatus, prometheus.GaugeValue, float64(counts[status]), status)
	}
}

// hitung job per status langsung dari migration_jobs
func countJobs(ctx context.Context, db *sql.DB) (map[string]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT status, count(*) FROM migration_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}
