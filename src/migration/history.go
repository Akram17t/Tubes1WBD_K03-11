package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ExecutionResponse memetakan model execution M1/M2 ke JSON tanpa mengekspos sql.Null*.
type ExecutionResponse struct {
	ID                int64       `json:"id,string"`
	DatabaseID        int64       `json:"database_id,string"`
	MigrationID       int64       `json:"migration_id,string"`
	ExecutedBy        int64       `json:"executed_by,string"`
	Revision          int         `json:"revision"`
	Operation         string      `json:"operation"`
	Attempt           int         `json:"attempt"`
	RequestID         *int64      `json:"request_id,string"`
	JobID             *int64      `json:"job_id,string"`
	SourceJobID       *int64      `json:"source_job_id,string"`
	TargetExecutionID *int64      `json:"target_execution_id,string"`
	HistorySequence   *int64      `json:"history_sequence,string"`
	SQLSnapshot       string      `json:"sql_snapshot"`
	Status            string      `json:"status"`
	Progress          JobProgress `json:"progress"`
	StartedAt         time.Time   `json:"started_at"`
	FinishedAt        *time.Time  `json:"finished_at"`
	ErrorMessage      *string     `json:"error_message"`
	SQLState          *string     `json:"sqlstate"`
}

type BatchAttempt struct {
	ID          int64  `json:"id,string"`
	ExecutionID int64  `json:"execution_id,string"`
	JobID       *int64 `json:"job_id,string"`
	BatchNumber int    `json:"batch_number"`
	Direction   string `json:"direction"`
	// Ini batas cakupan, bukan daftar ID; scope aktual tetap disimpan di ledger target.
	FirstID      *int64          `json:"first_id,string"`
	LastID       *int64          `json:"last_id,string"`
	SQLSnapshot  string          `json:"sql_snapshot"`
	Parameters   json.RawMessage `json:"parameters"`
	Status       string          `json:"status"`
	RowCount     *int64          `json:"row_count"`
	ErrorMessage *string         `json:"error_message"`
	SQLState     *string         `json:"sqlstate"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
}

type ExecutionDetail struct {
	ExecutionResponse
	BatchAttempts []BatchAttempt `json:"batch_attempts"`
	BatchPage     struct {
		Limit   int  `json:"limit"`
		Offset  int  `json:"offset"`
		HasMore bool `json:"has_more"`
	} `json:"batch_page"`
}

// executionResponse memakai snapshot attempt, bukan SQL dari definisi migration yang sekarang.
func executionResponse(databaseID int64, execution Execution) ExecutionResponse {
	result := ExecutionResponse{
		ID: execution.ID, DatabaseID: databaseID, MigrationID: execution.MigrationID,
		ExecutedBy: execution.ExecutedBy, Revision: execution.Revision,
		Operation: execution.Operation, Attempt: execution.Attempt,
		RequestID: execution.RequestID, JobID: execution.JobID, SourceJobID: execution.SourceJobID,
		TargetExecutionID: execution.TargetExecutionID, HistorySequence: execution.HistorySequence,
		SQLSnapshot: execution.SQLSnapshot, Status: execution.Status, Progress: execution.Progress,
		StartedAt: execution.StartedAt.UTC(), SQLState: execution.SQLState,
	}
	if execution.FinishedAt.Valid {
		finished := execution.FinishedAt.Time.UTC()
		result.FinishedAt = &finished
	}
	if execution.ErrorMessage.Valid {
		result.ErrorMessage = &execution.ErrorMessage.String
	}
	return result
}

// Cakupan database dibentuk sebelum mencari pasangan agar ID database lain tidak ikut terfilter.
const databaseHistory = `WITH database_executions AS (
	SELECT e.* FROM migration_executions e
	JOIN migrations m ON m.id = e.migration_id WHERE e.database_id = $1 AND m.database_id = $1
) `

// Pasangan hanya sah jika up/forward dan rollback sama-sama sukses pada migration yang sama.
const historyListSQL = databaseHistory + `, rollback_pairs AS (
	SELECT r.id, r.target_execution_id FROM database_executions r
	JOIN database_executions original ON original.id = r.target_execution_id
		AND original.migration_id = r.migration_id
	WHERE r.operation = 'rollback' AND r.status = 'success'
		AND original.status = 'success' AND original.operation IN ('up', 'forward')
)
SELECT ` + executionColumns + ` FROM database_executions e
WHERE NOT $2 OR e.id NOT IN (
	SELECT id FROM rollback_pairs UNION SELECT target_execution_id FROM rollback_pairs
)
ORDER BY started_at, id LIMIT $3 OFFSET $4`

// GetHistory membaca semua jenis attempt; Hide rollback hanya memengaruhi hasil baca.
func GetHistory(ctx context.Context, db *sql.DB, user *auth.User, databaseID int64, hideRollback bool, limit, offset int) (executions []Execution, hasMore bool, err error) {
	if databaseID < 1 || limit < 1 || limit > api.MaxLimit || offset < 0 {
		return nil, false, ErrInvalidInput
	}
	ctx, span := telemetry.Tracer().Start(ctx, "migration.history.list")
	span.SetAttributes(attribute.Int64("database_id", databaseID), attribute.Bool("hide_rollback", hideRollback))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "History tidak dapat dibaca")
		}
		span.End()
	}()
	if err := workflowAuthorize(ctx, db, user, auth.ViewMigration, databaseID); err != nil {
		return nil, false, err
	}
	args := []any{databaseID, hideRollback, limit + 1, offset}
	// QueryTracer dari koneksi pgx sudah mencatat query dan span SQL; tidak perlu log SQL kedua.
	rows, err := db.QueryContext(ctx, historyListSQL, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	executions = []Execution{}
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return nil, false, err
		}
		executions = append(executions, *execution)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// SQL telah menyembunyikan pasangan sebelum LIMIT; row tambahan menentukan has_more setelah filter.
	hasMore = len(executions) > limit
	if hasMore {
		executions = executions[:limit]
	}
	return executions, hasMore, nil
}

const historyDetailSQL = databaseHistory + `SELECT ` + executionColumns + ` FROM database_executions WHERE id = $2`

const batchAttemptsSQL = `SELECT id, execution_id, job_id, batch_number, direction,
	first_id, last_id, sql_snapshot,
	parameters, status, row_count, error_message, sqlstate, started_at, finished_at
	FROM migration_batch_attempts WHERE execution_id = $1
	ORDER BY started_at, id LIMIT $2 OFFSET $3`

// GetExecutionDetail membaca execution dan satu halaman batch dari snapshot database yang sama.
func GetExecutionDetail(ctx context.Context, db *sql.DB, user *auth.User, databaseID, executionID int64, limit, offset int) (detail *ExecutionDetail, err error) {
	if databaseID < 1 || executionID < 1 || limit < 1 || limit > api.MaxLimit || offset < 0 {
		return nil, ErrInvalidInput
	}
	ctx, span := telemetry.Tracer().Start(ctx, "migration.history.detail")
	span.SetAttributes(attribute.Int64("database_id", databaseID), attribute.Int64("execution_id", executionID))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "Detail execution tidak dapat dibaca")
		}
		span.End()
	}()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ViewMigration, databaseID); err != nil {
		return nil, err
	}
	args := []any{databaseID, executionID}
	execution, err := scanExecution(tx.QueryRowContext(ctx, historyDetailSQL, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	detail = &ExecutionDetail{ExecutionResponse: executionResponse(databaseID, *execution), BatchAttempts: []BatchAttempt{}}
	args = []any{executionID, limit + 1, offset}
	rows, err := tx.QueryContext(ctx, batchAttemptsSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var batch BatchAttempt
		if err := rows.Scan(&batch.ID, &batch.ExecutionID, &batch.JobID, &batch.BatchNumber, &batch.Direction,
			&batch.FirstID, &batch.LastID, &batch.SQLSnapshot, &batch.Parameters, &batch.Status, &batch.RowCount,
			&batch.ErrorMessage, &batch.SQLState, &batch.StartedAt, &batch.FinishedAt); err != nil {
			return nil, err
		}
		batch.StartedAt = batch.StartedAt.UTC()
		if batch.FinishedAt != nil {
			utc := batch.FinishedAt.UTC()
			batch.FinishedAt = &utc
		}
		detail.BatchAttempts = append(detail.BatchAttempts, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	detail.BatchPage.Limit, detail.BatchPage.Offset = limit, offset
	detail.BatchPage.HasMore = len(detail.BatchAttempts) > limit
	if detail.BatchPage.HasMore {
		detail.BatchAttempts = detail.BatchAttempts[:limit]
	}
	return detail, tx.Commit()
}
