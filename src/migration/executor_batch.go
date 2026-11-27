package migration

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"tubes-wbd/src/telemetry"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

//   - Scope ID dibekukan sekali per job up di _dbplatform_batch_scope dengan satu INSERT ... SELECT,
//     sekaligus diberi nomor batch berurutan menurut key. Aplikasi hanya memuat ID satu batch.
//   - Setiap batch adalah satu transaction target: UPDATE pengguna + baris ledger batch. Primary key
//     ledger (origin_job_id, direction, batch_number) mencegah batch diterapkan atau dibatalkan dua kali.
//   - Up/forward berjalan naik dari batch terakhir yang committed. Rollback/kompensasi berjalan turun atas
//     batch up committed milik origin job yang belum dibatalkan.
//   - Forward dan rollback memakai scope asli (scope_job_id), bukan row yang ada sekarang.

const batchLedgerColumns = `job_id, origin_job_id, execution_id, direction, batch_number,
	first_id, last_id, row_count, cursor_after`

type batchState struct {
	ScopeJobID   int64
	TotalBatches int
	Progress     JobProgress
}

func (e *Executor) runBatch(ctx context.Context, task *JobTask, plan *executionPlan, executionID int64) JobResult {
	progress := task.Progress
	if err := EnsureTargetMetadata(ctx, task.Target); err != nil {
		return e.failure(ctx, task, executionID, err, false, progress)
	}
	state, err := prepareBatch(ctx, task, plan)
	if err != nil {
		if planErr, ok := errors.AsType[planError](err); ok {
			return e.finish(task, executionID, JobResult{Status: stoppedStatus(task.Operation, progress),
				ErrorCode: "state_conflict", ErrorMessage: planErr.message}, "", nil, progress, nil)
		}
		return e.failure(ctx, task, executionID, err, false, progress)
	}
	progress = state.Progress
	for {
		if err := ctx.Err(); err != nil {
			return e.failure(ctx, task, executionID, err, false, progress)
		}
		number, ok, err := nextBatch(ctx, task, plan, state)
		if err != nil {
			return e.failure(ctx, task, executionID, err, false, progress)
		}
		if !ok {
			break
		}
		ids, err := scopeIDs(ctx, task.Target, state.ScopeJobID, number)
		if err != nil {
			return e.failure(ctx, task, executionID, err, false, progress)
		}
		if len(ids) == 0 {
			return e.finish(task, executionID, JobResult{Status: stoppedStatus(task.Operation, progress),
				ErrorCode: "state_conflict", ErrorMessage: "Scope batch asli tidak lengkap pada database target."},
				"", nil, progress, nil)
		}
		next, result, stop := e.applyBatch(ctx, task, plan, executionID, number, ids, progress)
		if stop {
			return result
		}
		progress = next
	}

	// Seluruh batch committed: tulis marker operasi selesai dalam transaction tersendiri.
	sequence, err := e.nextSequence(ctx, task)
	if err != nil {
		return e.failure(ctx, task, executionID, err, false, progress)
	}
	commitSent, err := e.targetTx(ctx, task, executionID, 0, func(ctx context.Context, tx *sql.Tx) error {
		return insertMarker(ctx, tx, task, executionID, sequence)
	})
	if err != nil {
		return e.failure(ctx, task, executionID, err, commitSent, progress)
	}
	return e.finish(task, executionID, JobResult{Status: JobSuccess}, "", &sequence, progress, nil)
}

// applyBatch mencatat batch attempt, menjalankan satu transaction target, lalu menyalin progress ke
// platform. stop=true berarti attempt berakhir dengan result.
func (e *Executor) applyBatch(ctx context.Context, task *JobTask, plan *executionPlan, executionID int64,
	number int, ids []int64, progress JobProgress) (JobProgress, JobResult, bool) {
	ctx, span := telemetry.Tracer().Start(ctx, "execution.batch", executionAttributes(task, executionID),
		trace.WithAttributes(attribute.Int("batch_number", number), attribute.String("direction", plan.Direction),
			attribute.Int("ids", len(ids))))
	attemptID, err := e.startBatch(ctx, task, plan, executionID, number, ids)
	if err != nil {
		endJobSpan(span, err)
		return progress, e.failure(ctx, task, executionID, err, false, progress), true
	}
	logExecution(ctx, slog.LevelInfo, "execution.batch_started", task, executionID, "batch_number", number,
		"direction", plan.Direction, "first_id", ids[0], "last_id", ids[len(ids)-1], "ids", len(ids))

	var rows int64
	// Batch up selalu milik job ini (forward punya ledger sendiri walau memakai scope asli); batch down
	// tercatat atas origin job agar satu batch asli tidak dapat dibatalkan dua kali.
	cursor, origin := ids[len(ids)-1], task.JobID
	if plan.Direction == "down" {
		cursor, origin = ids[0], plan.OriginJobID
	}
	commitSent, err := e.targetTx(ctx, task, executionID, number, func(ctx context.Context, tx *sql.Tx) error {
		// $1 adalah array ID batch; SQL sudah divalidasi hanya menyentuh key = ANY($1::bigint[]).
		result, err := tx.ExecContext(ctx, plan.SQL, ids)
		if err != nil {
			return err
		}
		if rows, err = result.RowsAffected(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO public._dbplatform_batch_ledger (`+batchLedgerColumns+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, task.JobID, origin, executionID,
			plan.Direction, number, ids[0], ids[len(ids)-1], rows, cursor)
		return err
	})
	endJobSpan(span, err)
	bookkeeping, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer cancel()
	if err != nil {
		if commitSent {
			// Batch attempt dibiarkan running; rekonsiliasi membacanya dari ledger.
			return progress, e.failure(ctx, task, executionID, err, true, progress), true
		}
		e.endBatch(bookkeeping, attemptID, "failed", 0, err)
		return progress, e.failure(ctx, task, executionID, err, false, progress), true
	}

	if plan.Direction == "up" {
		progress.CommittedBatches++
		progress.CommittedRows += rows
	} else {
		progress.RevertedBatches++
		progress.RevertedRows += rows
	}
	progress.LastCursor = &cursor
	logExecution(ctx, slog.LevelInfo, "execution.batch_committed", task, executionID, "batch_number", number,
		"direction", plan.Direction, "rows", rows)
	telemetry.BatchesCommitted.WithLabelValues(plan.Direction).Inc()
	telemetry.RowsCommitted.WithLabelValues(plan.Direction).Add(float64(rows))
	// Target sudah commit. Bila salinan platform gagal ditulis, rekonsiliasi melengkapinya dari ledger.
	if err := e.endBatch(bookkeeping, attemptID, "success", rows, nil); err == nil {
		err = e.saveProgress(bookkeeping, task, executionID, progress)
		if err == nil {
			return progress, JobResult{}, false
		}
	}
	return progress, JobResult{Status: JobOutcomeUnknown, ErrorCode: JobErrorInternal,
		ErrorMessage: "Batch sudah committed tetapi progress platform belum tercatat; akan dipulihkan dari ledger target."}, true
}

// prepareBatch membekukan scope (up) atau mendaftarkan scope asli (operasi lain), lalu membaca
// progress job dari ledger target sebagai sumber kebenaran.
func prepareBatch(ctx context.Context, task *JobTask, plan *executionPlan) (*batchState, error) {
	conn := task.Target
	if task.Operation == "up" {
		if err := freezeScope(ctx, conn, task.JobID, *plan.Batch); err != nil {
			return nil, err
		}
	} else if err := registerJob(ctx, conn, task, plan); err != nil {
		return nil, err
	}
	state := &batchState{}
	var total int64
	if err := conn.QueryRowContext(ctx, `SELECT scope_job_id, total_rows, total_batches
		FROM public._dbplatform_batch_jobs WHERE job_id = $1`, task.JobID).
		Scan(&state.ScopeJobID, &total, &state.TotalBatches); err != nil {
		return nil, err
	}
	progress, err := ledgerProgress(ctx, conn, task.JobID, plan.Direction, &total)
	state.Progress = progress
	return state, err
}

// freezeScope menyalin seluruh key tabel ke scope satu kali dengan operasi set-based.
func freezeScope(ctx context.Context, conn *sql.Conn, jobID int64, config BatchConfig) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO public._dbplatform_batch_jobs
		(job_id, scope_job_id, total_rows, total_batches) VALUES ($1, $1, 0, 0) ON CONFLICT (job_id) DO NOTHING`, jobID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count == 0 {
		return err // sudah dibekukan oleh attempt sebelumnya
	}
	table := pgx.Identifier{config.Schema, config.Table}.Sanitize()
	key := pgx.Identifier{config.KeyColumn}.Sanitize()
	if _, err := tx.ExecContext(ctx, `INSERT INTO public._dbplatform_batch_scope (job_id, id, batch_number)
		SELECT $1, scoped.key, ((row_number() OVER (ORDER BY scoped.key) - 1) / $2 + 1)::integer
		FROM (SELECT `+key+` AS key FROM `+table+`) scoped`, jobID, *config.BatchSize); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public._dbplatform_batch_jobs b
		SET total_rows = s.rows, total_batches = s.batches
		FROM (SELECT count(*) AS rows, COALESCE(max(batch_number), 0) AS batches
			FROM public._dbplatform_batch_scope WHERE job_id = $1) s
		WHERE b.job_id = $1`, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// registerJob mencatat scope asli untuk forward, rollback, atau kompensasi.
func registerJob(ctx context.Context, conn *sql.Conn, task *JobTask, plan *executionPlan) error {
	var scopeJobID, total int64
	var batches int
	err := conn.QueryRowContext(ctx, `SELECT scope_job_id, total_rows, total_batches
		FROM public._dbplatform_batch_jobs WHERE job_id = $1`, plan.OriginJobID).Scan(&scopeJobID, &total, &batches)
	if errors.Is(err, sql.ErrNoRows) {
		return planError{"Scope batch asli tidak ditemukan pada database target."}
	}
	if err != nil {
		return err
	}
	if plan.Direction == "down" {
		// Yang dibatalkan hanya batch up committed milik origin job.
		if err := conn.QueryRowContext(ctx, `SELECT COALESCE(sum(row_count), 0), count(*)
			FROM public._dbplatform_batch_ledger WHERE origin_job_id = $1 AND direction = 'up'`,
			plan.OriginJobID).Scan(&total, &batches); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO public._dbplatform_batch_jobs
		(job_id, scope_job_id, total_rows, total_batches) VALUES ($1, $2, $3, $4) ON CONFLICT (job_id) DO NOTHING`,
		task.JobID, scopeJobID, total, batches)
	return err
}

// nextBatch memilih batch berikutnya dari ledger, sehingga batch committed tidak pernah diulang.
func nextBatch(ctx context.Context, task *JobTask, plan *executionPlan, state *batchState) (int, bool, error) {
	var number sql.NullInt64
	var err error
	if plan.Direction == "up" {
		err = task.Target.QueryRowContext(ctx, `SELECT COALESCE(max(batch_number), 0) + 1
			FROM public._dbplatform_batch_ledger WHERE origin_job_id = $1 AND direction = 'up'`, task.JobID).Scan(&number)
		if err == nil && number.Int64 > int64(state.TotalBatches) {
			return 0, false, nil
		}
	} else {
		err = task.Target.QueryRowContext(ctx, `SELECT max(u.batch_number) FROM public._dbplatform_batch_ledger u
			WHERE u.origin_job_id = $1 AND u.direction = 'up' AND NOT EXISTS (
				SELECT 1 FROM public._dbplatform_batch_ledger d
				WHERE d.origin_job_id = u.origin_job_id AND d.direction = 'down' AND d.batch_number = u.batch_number)`,
			plan.OriginJobID).Scan(&number)
	}
	if err != nil || !number.Valid {
		return 0, false, err
	}
	return int(number.Int64), true, nil
}

func scopeIDs(ctx context.Context, conn *sql.Conn, scopeJobID int64, number int) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id FROM public._dbplatform_batch_scope
		WHERE job_id = $1 AND batch_number = $2 ORDER BY id`, scopeJobID, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ledgerProgress menghitung progress job dari ledger target.
func ledgerProgress(ctx context.Context, conn *sql.Conn, jobID int64, direction string, total *int64) (JobProgress, error) {
	progress := JobProgress{TotalRows: total}
	order := "DESC"
	if direction == "down" {
		order = "ASC"
	}
	var batches, rows int64
	var cursor sql.NullInt64
	err := conn.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(row_count), 0),
		(SELECT cursor_after FROM public._dbplatform_batch_ledger
			WHERE job_id = $1 AND direction = $2 ORDER BY batch_number `+order+` LIMIT 1)
		FROM public._dbplatform_batch_ledger WHERE job_id = $1 AND direction = $2`, jobID, direction).
		Scan(&batches, &rows, &cursor)
	if err != nil {
		return progress, err
	}
	if direction == "up" {
		progress.CommittedBatches, progress.CommittedRows = batches, rows
	} else {
		progress.RevertedBatches, progress.RevertedRows = batches, rows
	}
	if cursor.Valid {
		progress.LastCursor = &cursor.Int64
	}
	return progress, nil
}

// executionBatches membaca batch yang committed oleh satu execution untuk rekonsiliasi.
func executionBatches(ctx context.Context, conn *sql.Conn, jobID int64, direction string, executionID int64) ([]committedBatch, error) {
	rows, err := conn.QueryContext(ctx, `SELECT batch_number, row_count FROM public._dbplatform_batch_ledger
		WHERE job_id = $1 AND direction = $2 AND execution_id = $3 ORDER BY batch_number`, jobID, direction, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batches := []committedBatch{}
	for rows.Next() {
		var batch committedBatch
		if err := rows.Scan(&batch.Number, &batch.Rows); err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

func (e *Executor) startBatch(ctx context.Context, task *JobTask, plan *executionPlan, executionID int64,
	number int, ids []int64) (int64, error) {
	var id int64
	err := e.DB.QueryRowContext(ctx, `INSERT INTO migration_batch_attempts
		(execution_id, job_id, batch_number, direction, first_id, last_id, sql_snapshot, parameters)
		VALUES ($1, $2, $3, $4, $5, $6, $7, jsonb_build_object('ids', $8::integer)) RETURNING id`,
		executionID, task.JobID, number, plan.Direction, ids[0], ids[len(ids)-1], plan.SQL, len(ids)).Scan(&id)
	return id, err
}

func (e *Executor) endBatch(ctx context.Context, attemptID int64, status string, rows int64, failure error) error {
	var code, sqlState, message string
	if failure != nil {
		code, sqlState, message = JobErrorSQL, telemetry.SQLState(failure), telemetry.SafeError(failure)
		if sqlState == "" {
			code, message = JobErrorConnectionLost, "Batch dibatalkan sebelum commit."
		}
	}
	return exactlyOne(e.DB.ExecContext(ctx, `UPDATE migration_batch_attempts SET status = $2, row_count = $3,
		error_code = NULLIF($4, ''), sqlstate = NULLIF($5, ''), error_message = NULLIF($6, ''), finished_at = now()
		WHERE id = $1 AND status = 'running'`, attemptID, status, rows, code, sqlState, message))
}

// saveProgress menyalin progress ke platform. Update job dipagari worker token.
func (e *Executor) saveProgress(ctx context.Context, task *JobTask, executionID int64, p JobProgress) error {
	if err := fencedUpdate(e.DB.ExecContext(ctx, `
		UPDATE migration_jobs SET total_rows = $3, committed_rows = $4, committed_batches = $5,
			reverted_rows = $6, reverted_batches = $7, last_cursor = $8, updated_at = now()
		WHERE id = $1 AND worker_token = $2 AND status = 'running'`, task.JobID, task.WorkerToken,
		p.TotalRows, p.CommittedRows, p.CommittedBatches, p.RevertedRows, p.RevertedBatches, p.LastCursor)); err != nil {
		return err
	}
	if _, err := e.DB.ExecContext(ctx, `
		UPDATE migration_executions SET total_rows = $2, committed_rows = $3, committed_batches = $4,
			reverted_rows = $5, reverted_batches = $6, last_cursor = $7
		WHERE id = $1 AND status = 'running'`, executionID,
		p.TotalRows, p.CommittedRows, p.CommittedBatches, p.RevertedRows, p.RevertedBatches, p.LastCursor); err != nil {
		return err
	}
	if task.Operation == "compensate" && task.SourceJobID != nil {
		_, err := e.DB.ExecContext(ctx, `UPDATE migration_jobs SET reverted_rows = $2, reverted_batches = $3,
			updated_at = now() WHERE id = $1 AND status = 'partial'`, *task.SourceJobID, p.RevertedRows, p.RevertedBatches)
		return err
	}
	return nil
}
