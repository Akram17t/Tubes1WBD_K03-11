package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Executor adalah mesin eksekusi worker. SQL pengguna dan marker ledger selalu commit dalam satu
// transaction target; platform hanya menyimpan salinan hasil untuk API dan history.
type Executor struct {
	// DB adalah database platform.
	DB *sql.DB
}

// NewExecutor membuat eksekutor yang memenuhi kontrak JobExecutor.
func NewExecutor(db *sql.DB) *Executor {
	return &Executor{DB: db}
}

// executionPlan menurunkan SQL dan cakupan dari snapshot yang disetujui, bukan dari definisi terkini.
type executionPlan struct {
	SQL   string
	Batch *BatchConfig
	// Direction batch: up menerapkan batch, down membatalkan batch yang sudah committed.
	Direction string
	// OriginJobID adalah job yang batch up-nya dikerjakan: job itu sendiri untuk up, job execution
	// target untuk forward dan rollback, serta source job untuk kompensasi.
	OriginJobID int64
}

// planError adalah alasan pasti yang tidak akan berubah dengan mencoba ulang.
type planError struct{ message string }

func (e planError) Error() string { return e.message }

func (e *Executor) plan(ctx context.Context, task *JobTask) (*executionPlan, error) {
	snapshot := task.Snapshot
	plan := &executionPlan{Direction: "up"}
	switch task.Operation {
	case "up", "forward":
		plan.SQL = snapshot.SQLUp
	case "rollback", "compensate":
		if snapshot.Irreversible || snapshot.SQLDown == nil {
			return nil, planError{"Migration irreversible tidak dapat dibatalkan."}
		}
		plan.SQL, plan.Direction = *snapshot.SQLDown, "down"
		if task.Operation == "rollback" && task.TargetExecutionID == nil {
			return nil, planError{"Rollback tidak menunjuk execution yang dibatalkan."}
		}
	default:
		return nil, planError{"Operasi migration tidak dikenal."}
	}
	if snapshot.Mode != ModeBatch {
		if task.Operation == "compensate" {
			return nil, planError{"Kompensasi hanya berlaku untuk migration mode batch."}
		}
		return plan, nil
	}

	var config BatchConfig
	if err := json.Unmarshal(snapshot.BatchConfig, &config); err != nil {
		return nil, planError{"Konfigurasi batch pada snapshot tidak dapat dibaca."}
	}
	if config.BatchSize == nil || *config.BatchSize < 1 {
		size := defaultBatchSize
		config.BatchSize = &size
	}
	plan.Batch = &config
	switch task.Operation {
	case "up":
		plan.OriginJobID = task.JobID
	case "compensate":
		if task.SourceJobID == nil {
			return nil, planError{"Kompensasi tidak menunjuk source job."}
		}
		plan.OriginJobID = *task.SourceJobID
	case "rollback", "forward":
		if task.TargetExecutionID == nil {
			return nil, planError{"Operasi tidak menunjuk execution target."}
		}
		var jobID sql.NullInt64
		err := e.DB.QueryRowContext(ctx, `SELECT job_id FROM migration_executions WHERE id = $1 AND database_id = $2`,
			*task.TargetExecutionID, task.DatabaseID).Scan(&jobID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !jobID.Valid) {
			return nil, planError{"Execution target tidak memiliki scope batch."}
		}
		if err != nil {
			return nil, err
		}
		plan.OriginJobID = jobID.Int64
	}
	return plan, nil
}

// Execute menjalankan satu attempt. Row execution running di-commit sebelum SQL apa pun dikirim ke target.
func (e *Executor) Execute(ctx context.Context, task *JobTask) JobResult {
	plan, err := e.plan(ctx, task)
	if err != nil {
		return planFailure(task, err)
	}
	executionID, err := e.start(ctx, task, plan)
	if err != nil {
		// Commit row running mungkin sudah terjadi. Recovery memeriksanya; SQL belum dikirim.
		slog.ErrorContext(ctx, "execution.start_failed", "job_id", task.JobID, "attempt", task.Attempt,
			"worker_id", task.WorkerID, "error", telemetry.SafeError(err))
		return JobResult{Status: JobOutcomeUnknown, ErrorCode: JobErrorInternal,
			ErrorMessage: "Attempt tidak dapat dicatat; SQL belum dikirim dan status akan dipulihkan."}
	}
	logExecution(ctx, slog.LevelInfo, "execution.started", task, executionID, "mode", task.Snapshot.Mode)
	if plan.Batch != nil {
		return e.runBatch(ctx, task, plan, executionID)
	}
	return e.runNormal(ctx, task, plan, executionID)
}

func planFailure(task *JobTask, err error) JobResult {
	result := JobResult{Status: stoppedStatus(task.Operation, task.Progress)}
	if planErr, ok := errors.AsType[planError](err); ok {
		result.ErrorCode, result.ErrorMessage = "state_conflict", planErr.message
		return result
	}
	result.ErrorCode, result.ErrorMessage, result.Retryable = JobErrorInternal, "Rencana eksekusi tidak dapat dibaca.", true
	return result
}

// start mencatat attempt sebagai running beserta snapshot SQL yang akan benar-benar dijalankan.
func (e *Executor) start(ctx context.Context, task *JobTask, plan *executionPlan) (int64, error) {
	var target, traceID any
	if task.Operation == "rollback" {
		target = *task.TargetExecutionID
	}
	if span := trace.SpanContextFromContext(ctx); span.HasTraceID() {
		traceID = span.TraceID().String()
	}
	p := task.Progress
	var id int64
	err := e.DB.QueryRowContext(ctx, `
		INSERT INTO migration_executions (migration_id, executed_by, sql_snapshot, status, job_id, request_id,
			database_id, revision, operation, attempt, target_execution_id, worker_id, trace_id,
			total_rows, committed_rows, committed_batches, reverted_rows, reverted_batches, last_cursor)
		VALUES ($1, $2, $3, 'running', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id`,
		task.MigrationID, task.ActorID, plan.SQL, task.JobID, task.RequestID, task.DatabaseID, task.Revision,
		task.Operation, task.Attempt, target, task.WorkerID, traceID,
		p.TotalRows, p.CommittedRows, p.CommittedBatches, p.RevertedRows, p.RevertedBatches, p.LastCursor).Scan(&id)
	return id, err
}

// runNormal menjalankan SQL dan marker ledger dalam satu transaction target.
func (e *Executor) runNormal(ctx context.Context, task *JobTask, plan *executionPlan, executionID int64) JobResult {
	if err := EnsureTargetMetadata(ctx, task.Target); err != nil {
		return e.failure(ctx, task, executionID, err, false, task.Progress)
	}
	sequence, err := e.nextSequence(ctx, task)
	if err != nil {
		return e.failure(ctx, task, executionID, err, false, task.Progress)
	}
	sqlCtx, span := telemetry.Tracer().Start(ctx, "execution.sql", executionAttributes(task, executionID))
	commitSent, err := e.targetTx(sqlCtx, task, executionID, 0, func(ctx context.Context, tx *sql.Tx) error {
		// Blok DO menolak COMMIT/ROLLBACK di dalam SQL agar transaction eksekutor tidak terpecah.
		var command string
		if err := tx.QueryRowContext(ctx, `
			SELECT pg_catalog.format('DO %L', pg_catalog.format('BEGIN EXECUTE %L; END;', $1::text))`,
			plan.SQL).Scan(&command); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, command); err != nil {
			return err
		}
		return insertMarker(ctx, tx, task, executionID, sequence)
	})
	endJobSpan(span, err)
	if err != nil {
		return e.failure(ctx, task, executionID, err, commitSent, task.Progress)
	}
	return e.finish(task, executionID, JobResult{Status: JobSuccess}, "", &sequence, task.Progress, nil)
}

// nextSequence memberi nomor event sukses berikutnya. Lock database dipegang, jadi tidak ada penulis lain.
// Nomor M1 (version) dan isi ledger target ikut diperhitungkan agar marker tidak bertabrakan.
func (e *Executor) nextSequence(ctx context.Context, task *JobTask) (int64, error) {
	var platform, target int64
	if err := e.DB.QueryRowContext(ctx, `SELECT GREATEST(
		(SELECT COALESCE(MAX(history_sequence), 0) FROM migration_executions WHERE database_id = $1),
		(SELECT COALESCE(MAX(version), 0) FROM migrations WHERE database_id = $1))`, task.DatabaseID).Scan(&platform); err != nil {
		return 0, err
	}
	if err := task.Target.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM public._dbplatform_migrations`).Scan(&target); err != nil {
		return 0, err
	}
	return max(platform, target) + 1, nil
}

// insertMarker menulis bukti operasi selesai. target_execution_id hanya diisi untuk rollback karena
// forward atas execution yang sama akan melanggar unique index pembatalan.
func insertMarker(ctx context.Context, tx *sql.Tx, task *JobTask, executionID, sequence int64) error {
	var target any
	if task.Operation == "rollback" {
		target = *task.TargetExecutionID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO public._dbplatform_migrations
		(execution_id, migration_id, version, job_id, revision, operation, target_execution_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		executionID, task.MigrationID, sequence, task.JobID, task.Revision, task.Operation, target)
	return err
}

// targetTx menjalankan satu transaction target. commitSent berarti COMMIT sudah dikirim sehingga hanya
// ledger yang dapat membuktikan hasilnya.
func (e *Executor) targetTx(ctx context.Context, task *JobTask, executionID int64, batch int,
	work func(context.Context, *sql.Tx) error) (commitSent bool, err error) {
	extra := []any{}
	if batch > 0 {
		extra = append(extra, "batch_number", batch)
	}
	tx, err := task.Target.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	if err := work(ctx, tx); err != nil {
		tx.Rollback()
		logExecution(ctx, slog.LevelWarn, "execution.rollback", task, executionID, append(extra,
			"error", telemetry.SafeError(err), "sqlstate", telemetry.SQLState(err))...)
		return false, err
	}
	if err := ctx.Err(); err != nil {
		// COMMIT belum dikirim; transaction dibatalkan oleh database/sql.
		tx.Rollback()
		logExecution(ctx, slog.LevelWarn, "execution.rollback", task, executionID, append(extra, "error", "context dibatalkan")...)
		return false, err
	}
	_, span := telemetry.Tracer().Start(ctx, "execution.commit", executionAttributes(task, executionID))
	err = tx.Commit()
	endJobSpan(span, err)
	if err != nil {
		logExecution(ctx, slog.LevelError, "execution.commit", task, executionID, append(extra,
			"status", "error", "error", telemetry.SafeError(err))...)
		return true, err
	}
	logExecution(ctx, slog.LevelInfo, "execution.commit", task, executionID, append(extra, "status", "ok")...)
	return true, nil
}

// failure memetakan error satu attempt. Hasil tidak pasti dibiarkan running untuk rekonsiliasi.
func (e *Executor) failure(ctx context.Context, task *JobTask, executionID int64, err error, commitSent bool, progress JobProgress) JobResult {
	result := ClassifyExecutionError(err, commitSent)
	if !commitSent && ctx.Err() != nil {
		result = JobResult{Status: JobFailed, ErrorCode: JobErrorInterrupted,
			ErrorMessage: "Eksekusi dihentikan sebelum commit; transaction dibatalkan.", Retryable: true}
	}
	if result.Status == JobOutcomeUnknown {
		logExecution(ctx, slog.LevelError, "execution.outcome_unknown", task, executionID)
		return result
	}
	result.Status = stoppedStatus(task.Operation, progress)
	return e.finish(task, executionID, result, telemetry.SQLState(err), nil, progress, nil)
}

// committedBatch adalah batch milik attempt yang terbukti committed di ledger target.
type committedBatch struct {
	Number int
	Rows   int64
}

// finish menyiapkan Record: finalisasi row execution (sekali), salinan progress job, dan efek
// kompensasi pada source job. Dijalankan worker dalam transaction yang sama dengan status job.
func (e *Executor) finish(task *JobTask, executionID int64, result JobResult, sqlState string, sequence *int64,
	progress JobProgress, reconciled []committedBatch) JobResult {
	status := result.Status
	result.Record = func(ctx context.Context, tx *sql.Tx) error {
		p := progress
		if err := exactlyOne(tx.ExecContext(ctx, `
			UPDATE migration_executions SET status = $2, finished_at = now(), error_code = NULLIF($3, ''),
				error_message = NULLIF($4, ''), sqlstate = NULLIF($5, ''), history_sequence = $6,
				total_rows = $7, committed_rows = $8, committed_batches = $9, reverted_rows = $10,
				reverted_batches = $11, last_cursor = $12
			WHERE id = $1 AND status = 'running'`,
			executionID, status, result.ErrorCode, result.ErrorMessage, sqlState, sequence,
			p.TotalRows, p.CommittedRows, p.CommittedBatches, p.RevertedRows, p.RevertedBatches, p.LastCursor)); err != nil {
			return fmt.Errorf("finalisasi execution %d: %w", executionID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE migration_jobs SET total_rows = $2, committed_rows = $3, committed_batches = $4,
				reverted_rows = $5, reverted_batches = $6, last_cursor = $7, updated_at = now()
			WHERE id = $1`, task.JobID, p.TotalRows, p.CommittedRows, p.CommittedBatches,
			p.RevertedRows, p.RevertedBatches, p.LastCursor); err != nil {
			return err
		}
		if reconciled != nil {
			if err := recordReconciledBatches(ctx, tx, executionID, reconciled); err != nil {
				return err
			}
		}
		if task.Operation == "compensate" && task.SourceJobID != nil {
			return markSource(ctx, tx, *task.SourceJobID, p, status == JobSuccess)
		}
		return nil
	}
	return result
}

// markSource menyalin progress pembatalan ke source job. Kompensasi sukses menjadikannya compensated.
func markSource(ctx context.Context, tx *sql.Tx, sourceJobID int64, progress JobProgress, done bool) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE migration_jobs SET reverted_rows = $2, reverted_batches = $3,
			status = CASE WHEN $4 THEN 'compensated' ELSE status END,
			finished_at = CASE WHEN $4 THEN now() ELSE finished_at END, updated_at = now()
		WHERE id = $1 AND status = 'partial'`, sourceJobID, progress.RevertedRows, progress.RevertedBatches, done)
	if !done {
		return err
	}
	return exactlyOne(result, err)
}

// recordReconciledBatches menuntaskan batch attempt yang terputus sesuai bukti ledger.
func recordReconciledBatches(ctx context.Context, tx *sql.Tx, executionID int64, batches []committedBatch) error {
	for _, batch := range batches {
		if _, err := tx.ExecContext(ctx, `UPDATE migration_batch_attempts SET status = 'success', row_count = $3,
			finished_at = now() WHERE execution_id = $1 AND batch_number = $2 AND status = 'running'`,
			executionID, batch.Number, batch.Rows); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE migration_batch_attempts SET status = 'failed', finished_at = now(),
		error_code = $2, error_message = 'Attempt terputus; batch tidak tercatat committed di ledger target.'
		WHERE execution_id = $1 AND status = 'running'`, executionID, JobErrorInterrupted)
	return err
}

var errNotExactlyOne = errors.New("baris yang diperbarui tidak sesuai")

func exactlyOne(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errNotExactlyOne
	}
	return nil
}

// Reconcile menentukan hasil attempt yang terputus hanya dari ledger target. Tidak ada SQL pengguna.
func (e *Executor) Reconcile(ctx context.Context, task *JobTask) JobResult {
	unknown := JobResult{Status: JobOutcomeUnknown, ErrorCode: JobErrorOutcomeUnknown,
		ErrorMessage: "Bukti commit di database target belum dapat dibaca."}
	var executionID int64
	if err := e.DB.QueryRowContext(ctx, `SELECT id FROM migration_executions
		WHERE job_id = $1 AND attempt = $2 AND status = 'running'`, task.JobID, task.Attempt).Scan(&executionID); err != nil {
		return unknown
	}
	plan, err := e.plan(ctx, task)
	if err != nil {
		return unknown
	}
	ctx, span := telemetry.Tracer().Start(ctx, "execution.reconcile", executionAttributes(task, executionID))
	defer span.End()

	var hasLedger, hasBatch bool
	if err := task.Target.QueryRowContext(ctx, `SELECT
		pg_catalog.to_regclass('public._dbplatform_migrations') IS NOT NULL,
		pg_catalog.to_regclass('public._dbplatform_batch_ledger') IS NOT NULL
			AND pg_catalog.to_regclass('public._dbplatform_batch_jobs') IS NOT NULL`).Scan(&hasLedger, &hasBatch); err != nil {
		return unknown
	}
	var sequence *int64
	if hasLedger {
		var version int64
		err := task.Target.QueryRowContext(ctx, `SELECT version FROM public._dbplatform_migrations
			WHERE execution_id = $1 AND job_id = $2`, executionID, task.JobID).Scan(&version)
		switch {
		case err == nil:
			sequence = &version
		case !errors.Is(err, sql.ErrNoRows):
			return unknown
		}
	}
	progress, reconciled := task.Progress, []committedBatch{}
	if plan.Batch != nil && hasBatch {
		if progress, err = ledgerProgress(ctx, task.Target, task.JobID, plan.Direction, task.Progress.TotalRows); err != nil {
			return unknown
		}
		if reconciled, err = executionBatches(ctx, task.Target, task.JobID, plan.Direction, executionID); err != nil {
			return unknown
		}
	}
	logExecution(ctx, slog.LevelWarn, "execution.reconciled", task, executionID, "committed", sequence != nil)
	if sequence != nil {
		return e.finish(task, executionID, JobResult{Status: JobSuccess}, "", sequence, progress, reconciled)
	}
	// Bagian yang tidak tercatat di ledger pasti tidak committed, sehingga aman dilanjutkan dari checkpoint.
	result := JobResult{Status: stoppedStatus(task.Operation, progress), ErrorCode: JobErrorInterrupted,
		ErrorMessage: "Attempt terputus sebelum selesai; hasil dipulihkan dari ledger target.", Retryable: true}
	return e.finish(task, executionID, result, "", nil, progress, reconciled)
}

func executionAttributes(task *JobTask, executionID int64) trace.SpanStartOption {
	return trace.WithAttributes(attribute.Int64("job_id", task.JobID), attribute.Int64("execution_id", executionID),
		attribute.Int64("database_id", task.DatabaseID), attribute.Int("attempt", task.Attempt),
		attribute.String("operation", task.Operation))
}

func logExecution(ctx context.Context, level slog.Level, event string, task *JobTask, executionID int64, extra ...any) {
	attributes := []any{"job_id", task.JobID, "request_entry_id", task.RequestID, "database_id", task.DatabaseID,
		"migration_id", task.MigrationID, "revision", task.Revision, "operation", task.Operation,
		"attempt", task.Attempt, "execution_id", executionID, "worker_id", task.WorkerID}
	slog.Log(ctx, level, event, append(attributes, extra...)...)
}
