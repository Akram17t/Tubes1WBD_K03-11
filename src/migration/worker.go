package migration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"tubes-wbd/src/database"
	"tubes-wbd/src/telemetry"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// maxAutoAttempts membatasi attempt otomatis total per job.
	maxAutoAttempts    = 3
	lockRetryDelay     = 2 * time.Second
	finalizeTimeout    = 10 * time.Second
	defaultGracePeriod = 30 * time.Second
)

// Jeda sebelum attempt kedua dan ketiga.
var defaultRetryDelays = []time.Duration{time.Second, 5 * time.Second}

// errLeaseLost berarti job sudah diambil alih; worker lama tidak boleh menulis state.
var errLeaseLost = errors.New("klaim job sudah tidak berlaku")

// WorkerConfig mengatur satu proses worker.
type WorkerConfig struct {
	DB          *sql.DB
	Targets     database.TargetProvider
	Definitions DefinitionService
	Executor    JobExecutor

	Concurrency       int
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	StaleAfter        time.Duration
	// RetryDelays dan GracePeriod memakai default bila kosong.
	RetryDelays []time.Duration
	GracePeriod time.Duration
}

// Worker mengklaim job dari queue persisten dan menjalankannya satu per database.
type Worker struct {
	config WorkerConfig
	id     string
}

// NewWorker memvalidasi konfigurasi dan memberi identitas proses.
func NewWorker(config WorkerConfig) (*Worker, error) {
	if config.DB == nil || config.Targets == nil || config.Executor == nil ||
		config.Concurrency < 1 || config.PollInterval <= 0 || config.HeartbeatInterval <= 0 ||
		config.StaleAfter <= config.HeartbeatInterval {
		return nil, errors.New("konfigurasi worker tidak lengkap")
	}
	if len(config.RetryDelays) == 0 {
		config.RetryDelays = defaultRetryDelays
	}
	if config.GracePeriod <= 0 {
		config.GracePeriod = defaultGracePeriod
	}
	suffix, err := randomToken(6)
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		host = "worker"
	}
	return &Worker{config: config, id: fmt.Sprintf("%s-%d-%s", host, os.Getpid(), suffix)}, nil
}

func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Run bekerja sampai ctx dibatalkan. Setelah itu klaim baru dihentikan; job yang sedang berjalan
// diberi GracePeriod untuk selesai sebelum context-nya dibatalkan agar transaction di-rollback.
func (w *Worker) Run(ctx context.Context) {
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()
	slog.InfoContext(ctx, "worker.started", "worker_id", w.id, "concurrency", w.config.Concurrency)

	var group sync.WaitGroup
	loop := func(interval time.Duration, step func() (bool, error)) {
		defer group.Done()
		for ctx.Err() == nil {
			worked, err := step()
			if err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "worker.poll_failed", "worker_id", w.id, "error", telemetry.SafeError(err))
			}
			if worked && err == nil {
				continue
			}
			select {
			case <-ctx.Done():
			case <-time.After(interval):
			}
		}
	}
	group.Add(w.config.Concurrency + 1)
	for range w.config.Concurrency {
		go loop(w.config.PollInterval, func() (bool, error) { return w.RunOnce(jobCtx) })
	}
	go loop(w.config.HeartbeatInterval, func() (bool, error) {
		_, err := w.RecoverOnce(jobCtx)
		return false, err
	})

	<-ctx.Done()
	done := make(chan struct{})
	go func() { group.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(w.config.GracePeriod):
		slog.Warn("worker.grace_period_exceeded", "worker_id", w.id)
		cancelJobs()
		<-done
	}
	slog.Info("worker.stopped", "worker_id", w.id)
}

// claimedJob adalah salinan row job pada saat diklaim.
type claimedJob struct {
	JobTask
	AutoAttempts int
	EligibleAt   time.Time
	Carrier      map[string]string
}

const claimedColumns = `id, request_id, database_id, migration_id, revision, operation, attempt_count,
	execution_actor_id, total_rows, committed_rows, committed_batches, reverted_rows, reverted_batches,
	last_cursor, auto_attempt_count, next_attempt_at, trace_carrier`

func (w *Worker) scanClaim(row interface{ Scan(...any) error }, token string) (*claimedJob, error) {
	job := &claimedJob{}
	var carrier []byte
	if err := row.Scan(&job.JobID, &job.RequestID, &job.DatabaseID, &job.MigrationID, &job.Revision,
		&job.Operation, &job.Attempt, &job.ActorID, &job.Progress.TotalRows, &job.Progress.CommittedRows,
		&job.Progress.CommittedBatches, &job.Progress.RevertedRows, &job.Progress.RevertedBatches,
		&job.Progress.LastCursor, &job.AutoAttempts, &job.EligibleAt, &carrier); err != nil {
		return nil, err
	}
	// Carrier rusak hanya memutus sambungan trace, bukan menggagalkan job.
	_ = json.Unmarshal(carrier, &job.Carrier)
	job.WorkerID, job.WorkerToken = w.id, token
	return job, nil
}

// claim mengambil satu job dalam satu statement singkat. Database yang sedang berjalan atau
// hasilnya belum pasti dilewati sehingga database lain tetap diproses.
func (w *Worker) claim(ctx context.Context) (*claimedJob, error) {
	token, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	job, err := w.scanClaim(w.config.DB.QueryRowContext(ctx, `
		UPDATE migration_jobs SET status = 'running', worker_id = $1, worker_token = $2,
			heartbeat_at = now(), started_at = COALESCE(started_at, now()), finished_at = NULL, updated_at = now()
		WHERE id = (
			SELECT c.id FROM migration_jobs c
			WHERE c.status = 'queued' AND c.next_attempt_at <= now()
				AND NOT EXISTS (SELECT 1 FROM migration_jobs busy
					WHERE busy.database_id = c.database_id AND busy.status IN ('running', 'outcome_unknown'))
			ORDER BY c.next_attempt_at, c.id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING `+claimedColumns, w.id, token), token)
	if errors.Is(err, sql.ErrNoRows) || isUniqueViolation(err) {
		// Pelanggaran unik berarti worker lain lebih dulu mengklaim database yang sama.
		return nil, nil
	}
	return job, err
}

func isUniqueViolation(err error) bool {
	pgError, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgError.Code == "23505"
}

// heartbeat memperbarui klaim selama job dikerjakan. Klaim yang diambil alih membatalkan context.
func (w *Worker) heartbeat(ctx context.Context, job *claimedJob) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(w.config.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			result, err := w.config.DB.ExecContext(ctx, `UPDATE migration_jobs SET heartbeat_at = now()
				WHERE id = $1 AND worker_token = $2 AND status = 'running'`, job.JobID, job.WorkerToken)
			if err != nil {
				if ctx.Err() == nil {
					slog.WarnContext(ctx, "job.heartbeat_failed", "job_id", job.JobID, "worker_id", w.id, "error", telemetry.SafeError(err))
				}
				continue
			}
			if count, err := result.RowsAffected(); err == nil && count == 0 {
				cancel(errLeaseLost)
				return
			}
		}
	}()
	return ctx, func() { close(stop); group.Wait(); cancel(nil) }
}
