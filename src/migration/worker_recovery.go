package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"tubes-wbd/src/telemetry"
)

// Prosedur pemulihan lock: heartbeat basi atau outcome_unknown hanya menjadikan job kandidat. Job
// baru diambil alih setelah advisory lock platform (dan target, bila SQL mungkin sudah dikirim)
// berhasil diperoleh. Lock yang masih dipegang proses lama tidak pernah direbut berdasarkan waktu;
// PostgreSQL melepasnya ketika session pemiliknya berakhir.
const recoveryCandidates = `
	(status = 'running' AND heartbeat_at < now() - make_interval(secs => $1))
	OR (status = 'outcome_unknown' AND next_attempt_at <= now())`

// RecoverOnce memeriksa seluruh kandidat dan mengembalikan jumlah job yang statusnya diperbarui.
func (w *Worker) RecoverOnce(ctx context.Context) (int, error) {
	rows, err := w.config.DB.QueryContext(ctx,
		`SELECT id FROM migration_jobs WHERE `+recoveryCandidates+` ORDER BY id`, w.config.StaleAfter.Seconds())
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	recovered := 0
	var failures []error
	for _, id := range ids {
		changed, err := w.recoverJob(ctx, id)
		if errors.Is(err, errLeaseLost) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("job %d: %w", id, err))
			continue
		}
		if changed {
			recovered++
		}
	}
	return recovered, errors.Join(failures...)
}

func (w *Worker) recoverJob(ctx context.Context, jobID int64) (bool, error) {
	var databaseID int64
	if err := w.config.DB.QueryRowContext(ctx, `SELECT database_id FROM migration_jobs WHERE id = $1`,
		jobID).Scan(&databaseID); err != nil {
		return false, err
	}
	locks := &jobLocks{}
	defer locks.release()
	locked, err := w.lockPlatform(ctx, locks, databaseID)
	if err != nil || !locked {
		return false, err
	}

	// Ambil alih dengan token baru sehingga worker lama tidak bisa lagi menulis state.
	token, err := randomToken(16)
	if err != nil {
		return false, err
	}
	job, err := w.scanClaim(locks.platform.QueryRowContext(ctx, `
		UPDATE migration_jobs SET status = 'running', worker_id = $3, worker_token = $4,
			heartbeat_at = now(), started_at = COALESCE(started_at, now()), finished_at = NULL, updated_at = now()
		WHERE id = $2 AND (`+recoveryCandidates+`)
		RETURNING `+claimedColumns, w.config.StaleAfter.Seconds(), jobID, w.id, token), token)
	if errors.Is(err, sql.ErrNoRows) || isUniqueViolation(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ctx = jobContext(ctx, job)
	ctx, span := telemetry.Tracer().Start(ctx, "worker.recover", jobAttributes(job))
	defer span.End()
	w.logJob(ctx, slog.LevelWarn, "job.recovery_started", job, JobRunning)

	// Executor meng-commit row running sebelum mengirim SQL. Tanpa row itu, SQL belum pernah dikirim.
	err = locks.platform.QueryRowContext(ctx, `SELECT attempt FROM migration_executions
		WHERE job_id = $1 AND status = 'running'`, job.JobID).Scan(&job.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return true, w.failBeforeAttempt(ctx, locks, job, JobResult{ErrorCode: JobErrorInterrupted,
			ErrorMessage: "Worker berhenti sebelum SQL dikirim.", Retryable: true})
	}
	if err != nil {
		return false, err
	}

	ctx, stop := w.heartbeat(ctx, job)
	defer stop()
	request, err := scanRequest(locks.platform.QueryRowContext(ctx,
		`SELECT `+requestColumns+` FROM migration_requests WHERE id = $1`, job.RequestID))
	if err != nil {
		return false, err
	}
	job.Snapshot = request.Snapshot
	job.TargetExecutionID, job.SourceJobID = request.TargetExecutionID, request.SourceJobID

	locked, err = w.lockTarget(ctx, locks, databaseID)
	if err != nil {
		// Bukti belum dapat dibaca: pertahankan outcome unknown beserta blockernya.
		return true, w.finalize(ctx, locks.platform, job, JobResult{Status: JobOutcomeUnknown,
			ErrorCode: JobErrorTargetUnavailable, ErrorMessage: "Database target belum dapat diperiksa."})
	}
	if !locked {
		// Session lama masih memegang lock target, jadi transaction-nya mungkin masih berjalan.
		return true, w.finalize(ctx, locks.platform, job, JobResult{Status: JobOutcomeUnknown,
			ErrorCode: JobErrorOutcomeUnknown, ErrorMessage: "Database target masih dikunci proses lain."})
	}
	job.Target = locks.target
	result := w.config.Executor.Reconcile(ctx, &job.JobTask)
	return true, w.finalize(ctx, locks.platform, job, result)
}
