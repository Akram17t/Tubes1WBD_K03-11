package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	JobQueued            = "queued"
	JobRunning           = "running"
	JobSuccess           = "success"
	JobFailed            = "failed"
	JobCompensated       = "compensated"
	JobPartial           = "partial"
	JobPartiallyReverted = "partially_reverted"
	JobOutcomeUnknown    = "outcome_unknown"
)

// Kode error job yang aman ditampilkan. Penolakan revalidasi memakai code kontrak dari validator.
const (
	JobErrorTransient         = "transient_error"
	JobErrorConnectionLost    = "connection_lost"
	JobErrorTargetUnavailable = "target_unavailable"
	JobErrorInterrupted       = "worker_interrupted"
	JobErrorInternal          = "internal_error"
	JobErrorSQL               = "sql_error"
	JobErrorOutcomeUnknown    = "outcome_unknown"
)

// resumableJobErrors adalah penyebab di luar SQL. Job failed dengan penyebab lain (misalnya syntax
// atau constraint) tidak dapat di-resume karena SQL-nya immutable.
var resumableJobErrors = map[string]bool{
	JobErrorTransient: true, JobErrorConnectionLost: true, JobErrorTargetUnavailable: true,
	JobErrorInterrupted: true, JobErrorInternal: true, api.CodeDatabaseBlocked: true,
}

// JobService mengelola queue persisten pada database platform.
type JobService struct {
	DB          *sql.DB
	Definitions DefinitionService
}

// JobDetail adalah response API job. Tidak memuat SQL, konfigurasi, atau credential.
type JobDetail struct {
	Job
	ExecutionActorID  int64      `json:"execution_actor_id,string"`
	SourceJobID       *int64     `json:"source_job_id,string"`
	CompensationJobID *int64     `json:"compensation_job_id,string"`
	QueuedAt          time.Time  `json:"queued_at"`
	NextAttemptAt     *time.Time `json:"next_attempt_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	HistoryURL        string     `json:"history_url"`
}

const jobDetailColumns = `j.id, j.request_id, j.database_id, j.migration_id, j.revision, j.operation, j.status,
	j.attempt_count, j.total_rows, j.committed_rows, j.committed_batches, j.reverted_rows, j.reverted_batches,
	j.last_cursor, j.error_code, j.error_message, j.execution_actor_id, r.source_job_id, j.compensation_job_id,
	j.queued_at, j.next_attempt_at, j.started_at, j.finished_at,
	(SELECT e.sqlstate FROM migration_executions e WHERE e.job_id = j.id ORDER BY e.attempt DESC LIMIT 1)`

func scanJobDetail(row interface{ Scan(...any) error }) (*JobDetail, error) {
	var job JobDetail
	var errorCode, errorMessage, sqlState *string
	var nextAttempt time.Time
	err := row.Scan(&job.ID, &job.RequestID, &job.DatabaseID, &job.MigrationID, &job.Revision, &job.Operation,
		&job.Status, &job.AttemptCount, &job.Progress.TotalRows, &job.Progress.CommittedRows,
		&job.Progress.CommittedBatches, &job.Progress.RevertedRows, &job.Progress.RevertedBatches,
		&job.Progress.LastCursor, &errorCode, &errorMessage, &job.ExecutionActorID, &job.SourceJobID,
		&job.CompensationJobID, &job.QueuedAt, &nextAttempt, &job.StartedAt, &job.FinishedAt, &sqlState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	job.QueuedAt = job.QueuedAt.UTC()
	job.StartedAt, job.FinishedAt = utcPointer(job.StartedAt), utcPointer(job.FinishedAt)
	if job.Status == JobQueued {
		job.NextAttemptAt = utcPointer(&nextAttempt)
	}
	if errorCode != nil {
		job.Error = &JobError{Code: *errorCode}
		if errorMessage != nil {
			job.Error.Message = *errorMessage
		}
		if sqlState != nil {
			job.Error.SQLState = *sqlState
		}
	}
	job.BlockingDatabase = blocksDatabase(job.Operation, job.Status, job.Progress)
	job.AllowedActions = []string{}
	job.StatusURL = fmt.Sprintf("/api/databases/%d/jobs/%d", job.DatabaseID, job.ID)
	job.HistoryURL = fmt.Sprintf("/api/databases/%d/history", job.DatabaseID)
	return &job, nil
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// partialEffect melaporkan apakah job meninggalkan batch committed yang belum tuntas.
func partialEffect(operation string, progress JobProgress) bool {
	if operation == "rollback" || operation == "compensate" {
		return progress.RevertedBatches > 0
	}
	return progress.CommittedBatches > progress.RevertedBatches
}

// blocksDatabase sejalan dengan loadBlockers: status belum tuntas, atau antre/jalan dengan efek committed.
func blocksDatabase(operation, status string, progress JobProgress) bool {
	switch status {
	case JobPartial, JobPartiallyReverted, JobOutcomeUnknown:
		return true
	case JobQueued, JobRunning:
		return partialEffect(operation, progress)
	}
	return false
}

// stoppedStatus adalah status job yang berhenti dengan hasil pasti.
func stoppedStatus(operation string, progress JobProgress) string {
	if !partialEffect(operation, progress) {
		return JobFailed
	}
	if operation == "rollback" || operation == "compensate" {
		return JobPartiallyReverted
	}
	return JobPartial
}

func resumable(job *JobDetail) bool {
	if job.CompensationJobID != nil {
		return false // arah operasi sudah pindah ke job kompensasi
	}
	switch job.Status {
	case JobPartial, JobPartiallyReverted:
		return true
	case JobFailed:
		// Job kompensasi selalu dapat dilanjutkan: job sumbernya sudah terikat padanya, sehingga
		// tanpa resume database akan tertahan selamanya. Revalidasi tetap berlaku.
		if job.Operation == "compensate" {
			return true
		}
		return job.Error != nil && resumableJobErrors[job.Error.Code]
	}
	return false
}

func endJobSpan(span trace.Span, err error) {
	if err != nil {
		// Pesan error tidak direkam karena dapat memuat literal SQL atau data row.
		span.SetStatus(codes.Error, "operation failed")
	}
	span.End()
}

// traceCarrier hanya meneruskan W3C trace context agar header lain tidak ikut tersimpan.
func traceCarrier(carrier map[string]string) ([]byte, error) {
	clean := map[string]string{}
	for _, key := range []string{"traceparent", "tracestate"} {
		if value := carrier[key]; value != "" {
			clean[key] = value
		}
	}
	return json.Marshal(clean)
}

// Enqueue memenuhi RequestEnqueuer. Semua perubahan memakai transaction milik caller dan tidak
// di-commit di sini. Caller sudah mengunci migration serta request dan memeriksa approval.
func (s JobService) Enqueue(ctx context.Context, tx *sql.Tx, request MigrationRequest, actorID int64, carrier map[string]string) (*Job, error) {
	if tx == nil || request.ID < 1 || actorID < 1 {
		return nil, ErrInvalidInput
	}
	// UNIQUE(request_id) dan lock request membuat klik berulang mendapat job yang sama.
	existing, err := FindRequestJob(ctx, tx, request.ID)
	if err != nil || existing != nil {
		return existing, err
	}
	// Nilai dibaca dari row tersimpan, bukan dari struct caller.
	stored, err := scanRequest(tx.QueryRowContext(ctx,
		`SELECT `+requestColumns+` FROM migration_requests WHERE id = $1 FOR UPDATE`, request.ID))
	if err != nil {
		return nil, err
	}
	if stored.ReviewStatus != "approved" {
		return nil, ErrApprovalIncomplete
	}
	if stored.Operation == "compensate" {
		// Source job dikunci lalu diperiksa ulang agar hanya satu kompensasi yang menang.
		var existingID *int64
		var status string
		err := tx.QueryRowContext(ctx, `SELECT compensation_job_id, status FROM migration_jobs
			WHERE id = $1 AND database_id = $2 FOR UPDATE`, stored.SourceJobID, stored.DatabaseID).Scan(&existingID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if existingID != nil {
			return nil, compensationExists(*existingID)
		}
		if status != JobPartial {
			return nil, stateConflict("Kompensasi hanya untuk job yang berstatus partial.")
		}
	}
	encoded, err := traceCarrier(carrier)
	if err != nil {
		return nil, err
	}
	var jobID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO migration_jobs (request_id, database_id, migration_id, revision, operation,
			execution_actor_id, trace_carrier)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		stored.ID, stored.DatabaseID, stored.MigrationID, stored.Revision, stored.Operation, actorID, encoded,
	).Scan(&jobID); err != nil {
		return nil, err
	}
	if stored.Operation == "compensate" {
		if _, err := tx.ExecContext(ctx, `UPDATE migration_jobs SET compensation_job_id = $2, updated_at = now()
			WHERE id = $1`, stored.SourceJobID, jobID); err != nil {
			return nil, err
		}
	}
	// Enqueue pertama membekukan definisi, walaupun job nanti gagal sebelum SQL berjalan.
	if _, err := tx.ExecContext(ctx, `UPDATE migrations SET locked_at = now(), updated_at = now()
		WHERE id = $1 AND locked_at IS NULL`, stored.MigrationID); err != nil {
		return nil, err
	}
	// Commit dilakukan caller; hasil akhirnya tercatat pada event workflow.execute.
	slog.InfoContext(ctx, "job.enqueue_staged", "job_id", jobID, "request_entry_id", stored.ID,
		"database_id", stored.DatabaseID, "migration_id", stored.MigrationID, "revision", stored.Revision,
		"operation", stored.Operation, "execution_actor_id", actorID, "status", JobQueued)
	return FindRequestJob(ctx, tx, stored.ID)
}

func compensationExists(jobID int64) error {
	conflict := stateConflict("Source job sudah memiliki job kompensasi.").(*DefinitionError)
	conflict.Details = map[string]any{"existing_job_id": api.FormatID(jobID)}
	return conflict
}

func loadJobDetail(ctx context.Context, tx *sql.Tx, databaseID, jobID int64, lock bool) (*JobDetail, error) {
	query := `SELECT ` + jobDetailColumns + ` FROM migration_jobs j
		JOIN migration_requests r ON r.id = j.request_id WHERE j.database_id = $1 AND j.id = $2`
	if lock {
		query += ` FOR UPDATE OF j`
	}
	return scanJobDetail(tx.QueryRowContext(ctx, query, databaseID, jobID))
}

// decorate menghitung aksi untuk tampilan. Setiap mutasi tetap diperiksa ulang.
func (s JobService) decorate(ctx context.Context, tx *sql.Tx, userID int64, job *JobDetail) error {
	level, err := auth.EffectiveLevel(ctx, tx, userID, job.DatabaseID)
	if err != nil {
		return err
	}
	if level != auth.LevelEdit {
		return nil
	}
	if resumable(job) {
		job.AllowedActions = append(job.AllowedActions, "resume")
	}
	if job.Status != JobPartial || job.CompensationJobID != nil || (job.Operation != "up" && job.Operation != "forward") {
		return nil
	}
	var irreversible bool
	if err := tx.QueryRowContext(ctx, `SELECT irreversible FROM migrations WHERE id = $1`,
		job.MigrationID).Scan(&irreversible); err != nil {
		return err
	}
	if !irreversible {
		job.AllowedActions = append(job.AllowedActions, "request_compensate")
	}
	return nil
}

// Get membaca status job milik database pada URL tanpa mengubah apa pun.
func (s JobService) Get(ctx context.Context, user *auth.User, databaseID, jobID int64) (*JobDetail, error) {
	if databaseID < 1 || jobID < 1 {
		return nil, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ViewMigration, databaseID); err != nil {
		return nil, err
	}
	job, err := loadJobDetail(ctx, tx, databaseID, jobID, false)
	if err != nil {
		return nil, err
	}
	if err := s.decorate(ctx, tx, user.ID, job); err != nil {
		return nil, err
	}
	return job, tx.Commit()
}

// Resume mengantrekan kembali job yang sama. SQL, konfigurasi, checkpoint, dan approval tidak berubah;
// yang dicatat baru hanyalah actor resume, dan izinnya yang diperiksa worker.
func (s JobService) Resume(ctx context.Context, user *auth.User, databaseID, jobID int64) (job *JobDetail, err error) {
	if databaseID < 1 || jobID < 1 {
		return nil, ErrInvalidInput
	}
	ctx, span := telemetry.Tracer().Start(ctx, "workflow.resume")
	defer func() { endJobSpan(span, err) }()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ExecuteMigration, databaseID); err != nil {
		return nil, err
	}
	var requestID int64
	err = tx.QueryRowContext(ctx, `SELECT request_id FROM migration_jobs WHERE database_id = $1 AND id = $2`,
		databaseID, jobID).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Urutan lock sama dengan enqueue: migration, request, lalu job.
	request, err := lockRequest(ctx, tx, databaseID, requestID)
	if err != nil {
		return nil, err
	}
	current, err := loadJobDetail(ctx, tx, databaseID, jobID, true)
	if err != nil {
		return nil, err
	}
	if current.CompensationJobID != nil {
		return nil, compensationExists(*current.CompensationJobID)
	}
	if !resumable(current) {
		return nil, stateConflict("Job ini tidak dapat dilanjutkan pada keadaan sekarang.")
	}
	if _, err := checkLockedRequestApproval(ctx, tx, request, current.Revision); err != nil {
		return nil, err
	}
	if _, err := s.Definitions.validateOperation(ctx, tx, databaseID, requestInput(*request), jobID); err != nil {
		return nil, err
	}
	encoded, err := traceCarrier(telemetry.InjectCarrier(ctx))
	if err != nil {
		return nil, err
	}
	// auto_attempt_count tidak direset: resume tidak mengembalikan kuota retry otomatis.
	if _, err := tx.ExecContext(ctx, `
		UPDATE migration_jobs SET status = 'queued', execution_actor_id = $2, trace_carrier = $3,
			queued_at = now(), next_attempt_at = now(), finished_at = NULL, heartbeat_at = NULL,
			worker_id = NULL, worker_token = NULL, error_code = NULL, error_message = NULL, updated_at = now()
		WHERE id = $1`, jobID, user.ID, encoded); err != nil {
		return nil, err
	}
	job, err = loadJobDetail(ctx, tx, databaseID, jobID, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "job.resumed", "job_id", job.ID, "database_id", databaseID,
		"migration_id", job.MigrationID, "revision", job.Revision, "execution_actor_id", user.ID, "status", job.Status)
	return job, nil
}
