package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Job dipakai bersama oleh workflow dan API job, tanpa SQL atau credential.
type Job struct {
	ID               int64       `json:"id,string"`
	RequestID        int64       `json:"request_id,string"`
	DatabaseID       int64       `json:"database_id,string"`
	MigrationID      int64       `json:"migration_id,string"`
	Revision         int         `json:"revision"`
	Operation        string      `json:"operation"`
	Status           string      `json:"status"`
	AttemptCount     int         `json:"attempt_count"`
	Progress         JobProgress `json:"progress"`
	BlockingDatabase bool        `json:"blocking_database"`
	AllowedActions   []string    `json:"allowed_actions"`
	StatusURL        string      `json:"status_url"`
	Error            *JobError   `json:"error"`
}

type JobProgress struct {
	TotalRows        *int64 `json:"total_rows"`
	CommittedRows    int64  `json:"committed_rows"`
	CommittedBatches int64  `json:"committed_batches"`
	RevertedRows     int64  `json:"reverted_rows"`
	RevertedBatches  int64  `json:"reverted_batches"`
	LastCursor       *int64 `json:"last_cursor,string"`
}

type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Execute mengunci request supaya enqueue, cancel, dan klik berulang tidak bentrok.
// Pemeriksaan approval dan penyimpanan job memakai transaction yang sama.
func (s WorkflowService) Execute(ctx context.Context, user *auth.User, databaseID, requestID int64) (job *Job, err error) {
	if databaseID < 1 || requestID < 1 {
		return nil, ErrInvalidInput
	}
	if user != nil {
		ctx = telemetry.WithActor(ctx, user.ID)
	}
	ctx, span := telemetry.Tracer().Start(ctx, "workflow.execute")
	span.SetAttributes(attribute.Int64("database_id", databaseID), attribute.Int64("request_entry_id", requestID))
	// Error driver bisa memuat credential atau literal SQL, cukup catat statusnya.
	defer func() {
		status := "error"
		if err != nil {
			span.SetStatus(codes.Error, "Workflow execution request failed")
		}
		if job != nil {
			status = job.Status
			span.SetAttributes(attribute.Int64("job_id", job.ID))
		}
		span.SetAttributes(attribute.String("status", status))
		span.End()
		slog.InfoContext(ctx, "workflow.execute", "database_id", databaseID, "request_entry_id", requestID, "status", status)
	}()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ExecuteMigration, databaseID); err != nil {
		return nil, err
	}
	request, err := lockRequest(ctx, tx, databaseID, requestID)
	if err != nil {
		return nil, err
	}
	if _, err := checkLockedRequestApproval(ctx, tx, request, request.Revision); err != nil {
		return nil, err
	}
	if s.FindJob == nil {
		return nil, ErrWorkflowUnavailable
	}
	job, err = s.FindJob(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		if s.Enqueue == nil || s.Validate == nil {
			return nil, ErrWorkflowUnavailable
		}
		// Job baru perlu validasi ulang; klik duplikat tetap mengembalikan job lama.
		snapshot, err := s.validate(ctx, tx, databaseID, requestInput(*request))
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		var unchanged bool
		// JSONB membandingkan isi konfigurasi tanpa bergantung pada urutan key.
		if err := tx.QueryRowContext(ctx, `SELECT definition_snapshot = $2::jsonb
			FROM migration_requests WHERE id = $1`, request.ID, encoded).Scan(&unchanged); err != nil {
			return nil, err
		}
		if !unchanged {
			return nil, ErrConflict
		}
		enqueueCtx, enqueueSpan := telemetry.Tracer().Start(ctx, "workflow.enqueue")
		job, err = s.Enqueue(enqueueCtx, tx, *request, user.ID, telemetry.InjectCarrier(enqueueCtx))
		if err != nil {
			enqueueSpan.SetStatus(codes.Error, "Persistent enqueue failed")
		}
		enqueueSpan.End()
		if err != nil {
			return nil, err
		}
	}
	if job == nil || job.ID < 1 || job.RequestID != request.ID || job.DatabaseID != databaseID ||
		job.MigrationID != request.MigrationID || job.Revision != request.Revision || job.Operation != request.Operation {
		return nil, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Lengkapi field response tanpa mengubah objek milik queue.
	result := *job
	if result.AllowedActions == nil {
		result.AllowedActions = []string{}
	}
	result.StatusURL = fmt.Sprintf("/api/databases/%d/jobs/%d", databaseID, job.ID)
	return &result, nil
}
