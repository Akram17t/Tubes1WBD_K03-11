package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// jobLocks adalah dua koneksi khusus yang memegang advisory lock selama seluruh job, termasuk di
// antara commit batch. Koneksinya selalu dibuang agar lock tidak kembali ke pool.
type jobLocks struct {
	platform *sql.Conn
	target   *sql.Conn
}

func (l *jobLocks) release() {
	if l.target != nil {
		discardConnection(l.target)
	}
	if l.platform != nil {
		discardConnection(l.platform)
	}
}

// lockPlatform mengembalikan locked=false bila database sedang dikerjakan proses lain.
func (w *Worker) lockPlatform(ctx context.Context, locks *jobLocks, databaseID int64) (bool, error) {
	conn, err := w.config.DB.Conn(ctx)
	if err != nil {
		return false, err
	}
	locks.platform = conn
	var locked bool
	err = conn.QueryRowContext(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, databaseID).Scan(&locked)
	return locked, err
}

func (w *Worker) lockTarget(ctx context.Context, locks *jobLocks, databaseID int64) (bool, error) {
	pool, err := database.TargetConnection(ctx, w.config.DB, w.config.Targets, databaseID)
	if err != nil {
		return false, err
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return false, err
	}
	locks.target = conn
	var locked bool
	err = conn.QueryRowContext(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, databaseID).Scan(&locked)
	return locked, err
}

func jobAttributes(job *claimedJob) trace.SpanStartOption {
	return trace.WithAttributes(
		attribute.Int64("job_id", job.JobID), attribute.Int64("database_id", job.DatabaseID),
		attribute.Int64("migration_id", job.MigrationID), attribute.Int("revision", job.Revision),
		attribute.String("operation", job.Operation), attribute.String("worker_id", job.WorkerID))
}

func (w *Worker) logJob(ctx context.Context, level slog.Level, event string, job *claimedJob, status string, extra ...any) {
	attributes := []any{"job_id", job.JobID, "request_entry_id", job.RequestID, "database_id", job.DatabaseID,
		"migration_id", job.MigrationID, "revision", job.Revision, "operation", job.Operation,
		"attempt", job.Attempt, "worker_id", w.id, "status", status}
	slog.Log(ctx, level, event, append(attributes, extra...)...)
}

// jobContext menyambungkan trace API dari carrier dan mencatat actor eksekusi pada log.
func jobContext(ctx context.Context, job *claimedJob) context.Context {
	return telemetry.WithActor(telemetry.ExtractCarrier(ctx, job.Carrier), job.ActorID)
}

// RunOnce mengklaim dan memproses paling banyak satu job. false berarti tidak ada job yang layak.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	return w.runOnce(ctx, ctx)
}

// runOnce memakai stop untuk berhenti mengklaim. Klaim yang baru selesai setelah shutdown diminta
// dilepas kembali tanpa attempt, karena worker tidak lagi boleh memulai job baru.
func (w *Worker) runOnce(stop, ctx context.Context) (bool, error) {
	job, err := w.claim(ctx)
	if err != nil || job == nil {
		return false, err
	}
	if stop.Err() != nil {
		w.logJob(ctx, slog.LevelInfo, "job.released", job, JobQueued)
		return false, w.requeueAfter(ctx, job, 0)
	}
	ctx = jobContext(ctx, job)
	_, wait := telemetry.Tracer().Start(ctx, "queue.wait", trace.WithTimestamp(job.EligibleAt), jobAttributes(job))
	wait.End()
	ctx, span := telemetry.Tracer().Start(ctx, "worker.job", trace.WithSpanKind(trace.SpanKindConsumer), jobAttributes(job))
	w.logJob(ctx, slog.LevelInfo, "job.claimed", job, JobRunning,
		"queue_wait_ms", time.Since(job.EligibleAt).Milliseconds())
	ran, err := w.process(ctx, job)
	endJobSpan(span, err)
	if errors.Is(err, errLeaseLost) {
		w.logJob(ctx, slog.LevelWarn, "job.lease_lost", job, JobRunning)
		return true, nil
	}
	// ran=false membuat loop menunggu dulu supaya database yang terkunci tidak diklaim terus-menerus.
	return ran, err
}

func (w *Worker) process(ctx context.Context, job *claimedJob) (bool, error) {
	ctx, stop := w.heartbeat(ctx, job)
	defer stop()
	locks := &jobLocks{}
	defer locks.release()

	started := time.Now()
	lockCtx, lockSpan := telemetry.Tracer().Start(ctx, "worker.lock_wait", jobAttributes(job))
	locked, err := w.lockPlatform(lockCtx, locks, job.DatabaseID)
	if err != nil {
		// Database platform bermasalah: job dibiarkan running dan diambil recovery setelah basi.
		endJobSpan(lockSpan, err)
		return false, err
	}
	var targetErr error
	if locked {
		locked, targetErr = w.lockTarget(lockCtx, locks, job.DatabaseID)
	}
	endJobSpan(lockSpan, targetErr)
	waited := time.Since(started).Milliseconds()
	if targetErr != nil {
		w.logJob(ctx, slog.LevelWarn, "job.target_unavailable", job, JobRunning, "lock_wait_ms", waited)
		return false, w.failBeforeAttempt(ctx, locks, job, JobResult{ErrorCode: JobErrorTargetUnavailable,
			ErrorMessage: "Database target tidak dapat dihubungi sebelum eksekusi.", Retryable: true})
	}
	if !locked {
		// Bukan kegagalan eksekusi: tidak ada attempt dan kuota retry tidak berkurang.
		w.logJob(ctx, slog.LevelInfo, "job.lock_busy", job, JobQueued, "lock_wait_ms", waited)
		return false, w.requeue(ctx, job)
	}
	w.logJob(ctx, slog.LevelInfo, "job.locked", job, JobRunning, "lock_wait_ms", waited)

	snapshot, rejection := w.revalidate(ctx, locks.platform, job)
	if rejection != nil {
		w.logJob(ctx, slog.LevelWarn, "job.revalidation_rejected", job, JobRunning, "error_code", rejection.ErrorCode)
		return false, w.failBeforeAttempt(ctx, locks, job, *rejection)
	}
	if ctx.Err() != nil {
		return false, w.requeue(context.WithoutCancel(ctx), job)
	}
	job.Snapshot = *snapshot
	job.Target = locks.target
	if err := w.beginAttempt(ctx, locks.platform, job); err != nil {
		return false, err
	}
	w.logJob(ctx, slog.LevelInfo, "job.attempt_started", job, JobRunning)
	executeCtx, executeSpan := telemetry.Tracer().Start(ctx, "worker.execute", jobAttributes(job),
		trace.WithAttributes(attribute.Int("attempt", job.Attempt)))
	result := w.config.Executor.Execute(executeCtx, &job.JobTask)
	if result.Status != JobSuccess {
		executeSpan.SetStatus(codes.Error, result.ErrorCode)
	}
	executeSpan.End()
	return true, w.finalize(ctx, locks.platform, job, result)
}

// revalidate memeriksa ulang request, approval, actor, koneksi, posisi linear, dan blocker setelah
// lock dipegang. Aturan approval memakai fungsi workflow yang sama dengan API.
func (w *Worker) revalidate(ctx context.Context, platform *sql.Conn, job *claimedJob) (*DefinitionSnapshot, *JobResult) {
	ctx, span := telemetry.Tracer().Start(ctx, "worker.revalidate", jobAttributes(job))
	snapshot, err := w.checkJob(ctx, platform, job)
	endJobSpan(span, err)
	if err == nil {
		return snapshot, nil
	}
	if definitionErr, ok := errors.AsType[*DefinitionError](err); ok {
		return nil, &JobResult{ErrorCode: definitionErr.Code, ErrorMessage: definitionErr.Message,
			Retryable: definitionErr.Code == JobErrorTargetUnavailable}
	}
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return nil, &JobResult{ErrorCode: "forbidden",
			ErrorMessage: "Pelaksana tidak aktif atau tidak lagi punya izin Edit pada database aktif."}
	case errors.Is(err, ErrApprovalIncomplete):
		return nil, &JobResult{ErrorCode: "approval_incomplete", ErrorMessage: ErrApprovalIncomplete.Error()}
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidInput):
		return nil, &JobResult{ErrorCode: "state_conflict", ErrorMessage: "Pengajuan atau definisi tidak lagi sesuai dengan yang disetujui."}
	case errors.Is(err, database.ErrTargetUnavailable):
		return nil, &JobResult{ErrorCode: JobErrorTargetUnavailable, ErrorMessage: "Database target tidak dapat diperiksa.", Retryable: true}
	}
	slog.ErrorContext(ctx, "job.revalidation_failed", "job_id", job.JobID, "worker_id", w.id, "error", telemetry.SafeError(err))
	return nil, &JobResult{ErrorCode: JobErrorInternal, ErrorMessage: "Pemeriksaan ulang gagal karena kesalahan internal.", Retryable: true}
}

func (w *Worker) checkJob(ctx context.Context, platform *sql.Conn, job *claimedJob) (*DefinitionSnapshot, error) {
	tx, err := platform.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// workflowAuthorize di dalam CheckRequestApproval membaca ulang status aktif dan izin dari storage.
	actor := &auth.User{ID: job.ActorID}
	request, err := CheckRequestApproval(ctx, tx, actor, job.DatabaseID, job.RequestID, job.Revision)
	if err != nil {
		return nil, err
	}
	if request.MigrationID != job.MigrationID || request.Operation != job.Operation {
		return nil, ErrConflict
	}
	current, err := w.config.Definitions.validateOperation(ctx, tx, job.DatabaseID, requestInput(*request), job.JobID)
	if err != nil {
		return nil, err
	}
	same, err := sameSnapshot(current, request.Snapshot)
	if err != nil {
		return nil, err
	}
	if !same {
		return nil, ErrConflict
	}
	job.TargetExecutionID, job.SourceJobID = request.TargetExecutionID, request.SourceJobID
	approved := request.Snapshot
	return &approved, tx.Commit()
}

// sameSnapshot membandingkan isi tanpa bergantung pada urutan key atau spasi JSON.
func sameSnapshot(left, right DefinitionSnapshot) (bool, error) {
	var values [2]any
	for i, snapshot := range []DefinitionSnapshot{left, right} {
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(encoded, &values[i]); err != nil {
			return false, err
		}
	}
	return reflect.DeepEqual(values[0], values[1]), nil
}

// beginAttempt menaikkan nomor attempt. Dilakukan setelah revalidasi agar kegagalan sebelum SQL
// tidak muncul sebagai execution.
func (w *Worker) beginAttempt(ctx context.Context, platform *sql.Conn, job *claimedJob) error {
	err := platform.QueryRowContext(ctx, `
		UPDATE migration_jobs SET attempt_count = attempt_count + 1, auto_attempt_count = auto_attempt_count + 1,
			error_code = NULL, error_message = NULL, updated_at = now()
		WHERE id = $1 AND worker_token = $2 AND status = 'running'
		RETURNING attempt_count, auto_attempt_count`, job.JobID, job.WorkerToken).Scan(&job.Attempt, &job.AutoAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return errLeaseLost
	}
	return err
}

// requeue mengembalikan job tanpa menghitung attempt.
func (w *Worker) requeue(ctx context.Context, job *claimedJob) error {
	return w.requeueAfter(ctx, job, lockRetryDelay)
}

func (w *Worker) requeueAfter(ctx context.Context, job *claimedJob, delay time.Duration) error {
	return fencedUpdate(w.config.DB.ExecContext(ctx, `
		UPDATE migration_jobs SET status = 'queued', next_attempt_at = now() + make_interval(secs => $3),
			heartbeat_at = NULL, worker_id = NULL, worker_token = NULL, updated_at = now()
		WHERE id = $1 AND worker_token = $2 AND status = 'running'`,
		job.JobID, job.WorkerToken, delay.Seconds()))
}

func fencedUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errLeaseLost
	}
	return nil
}

// failBeforeAttempt dipakai ketika SQL belum dikirim, sehingga tidak ada row execution. Kegagalan
// sementara tetap menghabiskan kuota agar target yang mati tidak dicoba tanpa batas.
func (w *Worker) failBeforeAttempt(ctx context.Context, locks *jobLocks, job *claimedJob, result JobResult) error {
	if ctx.Err() != nil {
		if errors.Is(context.Cause(ctx), errLeaseLost) {
			return errLeaseLost
		}
		return w.requeue(context.WithoutCancel(ctx), job)
	}
	if result.Retryable {
		err := locks.platform.QueryRowContext(ctx, `
			UPDATE migration_jobs SET auto_attempt_count = auto_attempt_count + 1
			WHERE id = $1 AND worker_token = $2 AND status = 'running' RETURNING auto_attempt_count`,
			job.JobID, job.WorkerToken).Scan(&job.AutoAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return errLeaseLost
		}
		if err != nil {
			return err
		}
	}
	result.Status = stoppedStatus(job.Operation, job.Progress)
	return w.finalize(ctx, locks.platform, job, result)
}

// finalize menulis history dan status job dalam satu transaction pada koneksi pemegang lock.
// Jika gagal, job tetap running dan recovery membaca ledger target setelah heartbeat basi.
func (w *Worker) finalize(ctx context.Context, platform *sql.Conn, job *claimedJob, result JobResult) (err error) {
	// Hasil tetap dicatat walaupun context job sudah dibatalkan, misalnya saat shutdown.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer cancel()
	ctx, span := telemetry.Tracer().Start(ctx, "worker.finalize", jobAttributes(job))
	defer func() { endJobSpan(span, err) }()

	switch result.Status {
	case JobSuccess, JobFailed, JobPartial, JobPartiallyReverted, JobOutcomeUnknown:
	default:
		result = JobResult{Status: JobOutcomeUnknown, ErrorCode: JobErrorInternal,
			ErrorMessage: "Eksekutor mengembalikan status yang tidak dikenal."}
	}
	tx, err := platform.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Row job dikunci dan token diperiksa sebelum history disentuh.
	var autoAttempts int
	err = tx.QueryRowContext(ctx, `SELECT auto_attempt_count FROM migration_jobs
		WHERE id = $1 AND worker_token = $2 AND status = 'running' FOR UPDATE`,
		job.JobID, job.WorkerToken).Scan(&autoAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return errLeaseLost
	}
	if err != nil {
		return err
	}
	if result.Record != nil {
		if err := result.Record(ctx, tx); err != nil {
			return err
		}
	}

	status, delay := result.Status, time.Duration(0)
	retry := result.Retryable && status != JobSuccess && status != JobOutcomeUnknown && autoAttempts < maxAutoAttempts
	switch {
	case retry:
		status = JobQueued
		delay = w.config.RetryDelays[min(max(autoAttempts-1, 0), len(w.config.RetryDelays)-1)]
	case status == JobOutcomeUnknown:
		// next_attempt_at menjadi jadwal pemeriksaan ledger berikutnya.
		delay = w.config.HeartbeatInterval
	}
	finished := status != JobQueued && status != JobOutcomeUnknown
	if status == JobSuccess {
		result.ErrorCode, result.ErrorMessage = "", ""
	}
	if err := fencedUpdate(tx.ExecContext(ctx, `
		UPDATE migration_jobs SET status = $3, error_code = NULLIF($4, ''), error_message = NULLIF($5, ''),
			next_attempt_at = now() + make_interval(secs => $6),
			finished_at = CASE WHEN $7 THEN now() END,
			heartbeat_at = NULL, worker_token = NULL, updated_at = now()
		WHERE id = $1 AND worker_token = $2 AND status = 'running'`,
		job.JobID, job.WorkerToken, status, result.ErrorCode, result.ErrorMessage, delay.Seconds(), finished)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	level := slog.LevelInfo
	if result.Status != JobSuccess {
		level = slog.LevelWarn
	}
	w.logJob(ctx, level, "job.attempt_finished", job, result.Status, "error_code", result.ErrorCode)
	if retry {
		w.logJob(ctx, slog.LevelInfo, "job.retry_scheduled", job, JobQueued, "delay_ms", delay.Milliseconds())
	}
	return nil
}
