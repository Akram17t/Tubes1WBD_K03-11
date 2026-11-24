package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"tubes-wbd/src/auth"
)

var (
	ErrWorkflowUnavailable = errors.New("integrasi definisi atau job migration belum tersedia")
	ErrApprovalIncomplete  = errors.New("persetujuan group belum lengkap")
)

type RequestInput struct {
	MigrationID       int64  `json:"migration_id,string"`
	Revision          int    `json:"revision"`
	Operation         string `json:"operation"`
	TargetExecutionID *int64 `json:"target_execution_id,string"`
	SourceJobID       *int64 `json:"source_job_id,string"`
}

type DefinitionSnapshot struct {
	MigrationID        int64           `json:"migration_id,string"`
	DatabaseID         int64           `json:"database_id,string"`
	Revision           int             `json:"revision"`
	Title              string          `json:"title"`
	Description        *string         `json:"description"`
	SQLUp              string          `json:"sql_up"`
	SQLDown            *string         `json:"sql_down"`
	Irreversible       bool            `json:"irreversible"`
	IrreversibleReason *string         `json:"irreversible_reason"`
	Mode               string          `json:"mode"`
	BatchConfig        json.RawMessage `json:"batch_config"`
}

type MigrationRequest struct {
	ID                int64              `json:"id,string"`
	DatabaseID        int64              `json:"database_id,string"`
	MigrationID       int64              `json:"migration_id,string"`
	Revision          int                `json:"revision"`
	Operation         string             `json:"operation"`
	TargetExecutionID *int64             `json:"target_execution_id,string"`
	SourceJobID       *int64             `json:"source_job_id,string"`
	ReviewStatus      string             `json:"review_status"`
	RequestedBy       int64              `json:"requested_by,string"`
	Snapshot          DefinitionSnapshot `json:"snapshot"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	SubmittedAt       *time.Time         `json:"submitted_at"`
}

type RequestApproval struct {
	GroupID          int64      `json:"group_id,string"`
	GroupName        string     `json:"group_name"`
	ApprovedBy       *int64     `json:"approved_by,string"`
	ApprovedAt       *time.Time `json:"approved_at"`
	WaitingForLeader bool       `json:"waiting_for_leader"`
	CanApprove       bool       `json:"can_approve"`
}

type RequestDetail struct {
	MigrationRequest
	WorkflowStatus  string            `json:"workflow_status"`
	Approvals       []RequestApproval `json:"approvals"`
	PendingGroupIDs []string          `json:"pending_group_ids"`
	CanApprove      bool              `json:"can_approve"`
	AllowedActions  []string          `json:"allowed_actions"`
	JobID           *int64            `json:"job_id,string"`
}

type ApprovalResult struct {
	RequestID        int64    `json:"request_id,string"`
	ReviewStatus     string   `json:"review_status"`
	ApprovedGroupIDs []string `json:"approved_group_ids"`
}

// RequestValidator membaca definisi tersimpan dan memvalidasi SQL/operasi tanpa mengeksekusinya.
type RequestValidator func(context.Context, *sql.Tx, int64, RequestInput) (DefinitionSnapshot, error)

// Baca job dalam transaction caller tanpa melakukan commit.
type RequestJobLookup func(context.Context, *sql.Tx, int64) (*Job, error)

// RequestEnqueuer menyimpan job dari snapshot approved dalam transaction caller.
// Pemilik queue memeriksa state linear dan UNIQUE(request_id), tanpa commit atau eksekusi SQL pengguna.
type RequestEnqueuer func(context.Context, *sql.Tx, MigrationRequest, int64, map[string]string) (*Job, error)

type WorkflowService struct {
	DB       *sql.DB
	Validate RequestValidator
	FindJob  RequestJobLookup
	Enqueue  RequestEnqueuer
}

type workflowQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const requestColumns = `id, database_id, migration_id, revision, operation,
	target_execution_id, source_job_id, review_status, requested_by,
	definition_snapshot, created_at, updated_at, submitted_at`

// validRequestInput memastikan operasi memakai ID, revisi, dan referensi target yang sesuai.
func validRequestInput(input RequestInput) bool {
	if input.MigrationID < 1 || input.Revision < 1 ||
		(input.TargetExecutionID != nil && *input.TargetExecutionID < 1) ||
		(input.SourceJobID != nil && *input.SourceJobID < 1) {
		return false
	}
	switch input.Operation {
	case "up":
		return input.TargetExecutionID == nil && input.SourceJobID == nil
	case "rollback", "forward":
		return input.TargetExecutionID != nil && input.SourceJobID == nil
	case "compensate":
		return input.TargetExecutionID == nil && input.SourceJobID != nil
	}
	return false
}

// requestInput mengambil referensi operasi dari request tersimpan tanpa input SQL dari client.
func requestInput(request MigrationRequest) RequestInput {
	return RequestInput{request.MigrationID, request.Revision, request.Operation, request.TargetExecutionID, request.SourceJobID}
}

// workflowAuthorize memeriksa akun aktif dan hak akses database untuk aksi workflow.
func workflowAuthorize(ctx context.Context, db workflowQueryer, user *auth.User, action auth.Action, databaseID int64) error {
	if user == nil {
		return auth.ErrForbidden
	}
	var active bool
	if err := db.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id = $1`, user.ID).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.ErrForbidden
		}
		return err
	}
	if !active {
		return auth.ErrForbidden
	}
	return auth.Authorize(ctx, db, user, action, databaseID)
}

// scanRequest membaca snapshot dan mengubah waktu ke UTC tanpa mengekspos tipe nullable SQL.
func scanRequest(row interface{ Scan(...any) error }) (*MigrationRequest, error) {
	var request MigrationRequest
	var snapshot []byte
	err := row.Scan(&request.ID, &request.DatabaseID, &request.MigrationID, &request.Revision,
		&request.Operation, &request.TargetExecutionID, &request.SourceJobID, &request.ReviewStatus,
		&request.RequestedBy, &snapshot, &request.CreatedAt, &request.UpdatedAt, &request.SubmittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(snapshot, &request.Snapshot); err != nil {
		return nil, err
	}
	request.CreatedAt = request.CreatedAt.UTC()
	request.UpdatedAt = request.UpdatedAt.UTC()
	if request.SubmittedAt != nil {
		utc := request.SubmittedAt.UTC()
		request.SubmittedAt = &utc
	}
	return &request, nil
}

// lockRequestMigration memastikan migration milik database yang diminta lalu menguncinya.
func lockRequestMigration(ctx context.Context, tx *sql.Tx, databaseID, migrationID int64) error {
	var id int64
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM migrations WHERE database_id = $1 AND id = $2 FOR UPDATE`, databaseID, migrationID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// lockRequest mengunci migration sebelum request agar urutannya sama dengan perubahan definisi.
func lockRequest(ctx context.Context, tx *sql.Tx, databaseID, requestID int64) (*MigrationRequest, error) {
	var migrationID int64
	err := tx.QueryRowContext(ctx, `SELECT migration_id FROM migration_requests WHERE database_id = $1 AND id = $2`, databaseID, requestID).Scan(&migrationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Urutan lock disamakan dengan perubahan definisi agar tidak deadlock.
	if err := lockRequestMigration(ctx, tx, databaseID, migrationID); err != nil {
		return nil, err
	}
	return scanRequest(tx.QueryRowContext(ctx, `SELECT `+requestColumns+`
		FROM migration_requests WHERE database_id = $1 AND id = $2 FOR UPDATE`, databaseID, requestID))
}

// validate meminta snapshot terpercaya dari validator Manuel dan mencocokkan identitas revisinya.
func (s WorkflowService) validate(ctx context.Context, tx *sql.Tx, databaseID int64, input RequestInput) (DefinitionSnapshot, error) {
	if s.Validate == nil {
		return DefinitionSnapshot{}, ErrWorkflowUnavailable
	}
	snapshot, err := s.Validate(ctx, tx, databaseID, input)
	if err != nil {
		return DefinitionSnapshot{}, err
	}
	if snapshot.DatabaseID != databaseID || snapshot.MigrationID != input.MigrationID || snapshot.Revision != input.Revision {
		return DefinitionSnapshot{}, ErrConflict
	}
	return snapshot, nil
}

// Create menyimpan pengajuan draft dari definisi tersimpan setelah pemeriksaan izin Edit.
func (s WorkflowService) Create(ctx context.Context, user *auth.User, databaseID int64, input RequestInput) (*MigrationRequest, error) {
	if databaseID < 1 || !validRequestInput(input) {
		return nil, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Pastikan izin dan kepemilikan sebelum validator membaca definisi yang dikunci.
	if err := workflowAuthorize(ctx, tx, user, auth.CreateMigration, databaseID); err != nil {
		return nil, err
	}
	if err := lockRequestMigration(ctx, tx, databaseID, input.MigrationID); err != nil {
		return nil, err
	}
	snapshot, err := s.validate(ctx, tx, databaseID, input)
	if err != nil {
		return nil, err
	}
	// Simpan SQL/config dari storage sebagai snapshot yang akan ditinjau leader.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	request, err := scanRequest(tx.QueryRowContext(ctx, `
		INSERT INTO migration_requests (database_id, migration_id, revision, operation,
			target_execution_id, source_job_id, requested_by, definition_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+requestColumns,
		databaseID, input.MigrationID, input.Revision, input.Operation,
		input.TargetExecutionID, input.SourceJobID, user.ID, encoded))
	if err != nil {
		return nil, err
	}
	return request, tx.Commit()
}

// Submit memvalidasi ulang snapshot dan membekukan daftar group yang wajib menyetujui pengajuan.
func (s WorkflowService) Submit(ctx context.Context, user *auth.User, databaseID, requestID int64) (*MigrationRequest, error) {
	if databaseID < 1 || requestID < 1 {
		return nil, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.EditMigration, databaseID); err != nil {
		return nil, err
	}
	// Urutan lock harus sama dengan editor definisi agar snapshot tidak berubah saat submit.
	request, err := lockRequest(ctx, tx, databaseID, requestID)
	if err != nil {
		return nil, err
	}
	if request.RequestedBy != user.ID {
		return nil, auth.ErrForbidden
	}
	if request.ReviewStatus != "draft" {
		return nil, ErrConflict
	}
	snapshot, err := s.validate(ctx, tx, databaseID, requestInput(*request))
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	// Group tanpa leader tetap mendapat slot agar pengajuan tidak lolos tanpa persetujuannya.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO migration_approvals (request_id, group_id, group_name_snapshot)
		SELECT $1, g.id, g.name FROM groups g
		JOIN group_database_access a ON a.group_id = g.id
		WHERE a.database_id = $2 AND a.access_level IN ('view', 'edit') AND NOT g.is_archived`, requestID, databaseID)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrApprovalIncomplete
	}
	// Snapshot, slot approval, dan status review harus commit bersama.
	request, err = scanRequest(tx.QueryRowContext(ctx, `
		UPDATE migration_requests SET review_status = 'pending_review', definition_snapshot = $2,
			submitted_at = now(), updated_at = now() WHERE id = $1 RETURNING `+requestColumns, requestID, encoded))
	if err != nil {
		return nil, err
	}
	return request, tx.Commit()
}

// Approve mengisi seluruh slot group yang dipimpin aktor dalam satu transaction dan aman diulang.
func (s WorkflowService) Approve(ctx context.Context, user *auth.User, databaseID, requestID int64) (*ApprovalResult, error) {
	if databaseID < 1 || requestID < 1 {
		return nil, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ViewMigration, databaseID); err != nil {
		return nil, err
	}
	request, err := lockRequest(ctx, tx, databaseID, requestID)
	if err != nil {
		return nil, err
	}
	if request.ReviewStatus != "pending_review" && request.ReviewStatus != "approved" {
		return nil, ErrConflict
	}
	// Kunci group dan keanggotaan yang diwakili agar pergantian leader tidak mendahului tanda tangan.
	rows, err := tx.QueryContext(ctx, `
		SELECT g.id FROM groups g JOIN migration_approvals a ON a.group_id = g.id
		JOIN user_groups ug ON ug.group_id = g.id AND ug.user_id = $2
		WHERE a.request_id = $1 AND g.leader_user_id = $2 AND NOT g.is_archived
		ORDER BY g.id FOR SHARE OF g, ug`, requestID, user.ID)
	if err != nil {
		return nil, err
	}
	represented := 0
	for rows.Next() {
		represented++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if represented == 0 {
		return nil, auth.ErrForbidden
	}
	// Isi hanya slot kosong supaya klik berulang tidak menimpa tanda tangan sebelumnya.
	rows, err = tx.QueryContext(ctx, `
		UPDATE migration_approvals a SET approved_by = $2, approved_at = now()
		FROM groups g JOIN user_groups ug ON ug.group_id = g.id AND ug.user_id = $2
		JOIN users u ON u.id = ug.user_id AND u.is_active
		WHERE a.request_id = $1 AND a.group_id = g.id AND a.approved_by IS NULL
			AND g.leader_user_id = $2 AND NOT g.is_archived
		RETURNING a.group_id::text`, requestID, user.ID)
	if err != nil {
		return nil, err
	}
	result := &ApprovalResult{RequestID: requestID, ApprovedGroupIDs: []string{}}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		result.ApprovedGroupIDs = append(result.ApprovedGroupIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// Approval dihitung per group dan daftar kosong tidak boleh dianggap lengkap.
	err = tx.QueryRowContext(ctx, `
		UPDATE migration_requests SET review_status = CASE WHEN
			EXISTS (SELECT 1 FROM migration_approvals WHERE request_id = $1) AND
			NOT EXISTS (SELECT 1 FROM migration_approvals WHERE request_id = $1 AND approved_by IS NULL)
			THEN 'approved' ELSE 'pending_review' END, updated_at = now()
		WHERE id = $1 RETURNING review_status`, requestID).Scan(&result.ReviewStatus)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

// Cancel menarik pengajuan milik aktor setelah memastikan belum ada job yang dibuat.
func (s WorkflowService) Cancel(ctx context.Context, user *auth.User, databaseID, requestID int64) (*MigrationRequest, error) {
	if databaseID < 1 || requestID < 1 {
		return nil, ErrInvalidInput
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.EditMigration, databaseID); err != nil {
		return nil, err
	}
	request, err := lockRequest(ctx, tx, databaseID, requestID)
	if err != nil {
		return nil, err
	}
	if request.RequestedBy != user.ID {
		return nil, auth.ErrForbidden
	}
	if request.ReviewStatus == "cancelled" {
		return request, tx.Commit()
	}
	if s.FindJob == nil {
		return nil, ErrWorkflowUnavailable
	}
	// Lock request tetap ditahan agar enqueue tidak menyelip setelah pemeriksaan job.
	job, err := s.FindJob(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	if job != nil {
		return nil, ErrConflict
	}
	request, err = scanRequest(tx.QueryRowContext(ctx, `
		UPDATE migration_requests SET review_status = 'cancelled', updated_at = now()
		WHERE id = $1 RETURNING `+requestColumns, requestID))
	if err != nil {
		return nil, err
	}
	return request, tx.Commit()
}

// HasActiveReview memeriksa pengajuan aktif setelah caller mengunci migration yang akan diedit.
func HasActiveReview(ctx context.Context, tx *sql.Tx, migrationID int64) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM migration_requests WHERE migration_id = $1 AND review_status IN ('pending_review', 'approved')
	)`, migrationID).Scan(&active)
	return active, err
}

// CheckRequestApproval memeriksa izin Edit, revisi, dan approval untuk enqueue serta worker.
// Transaction tetap dimiliki caller dan tidak di-commit oleh pemeriksaan ini.
func CheckRequestApproval(ctx context.Context, tx *sql.Tx, user *auth.User, databaseID, requestID int64, revision int) (*MigrationRequest, error) {
	if databaseID < 1 || requestID < 1 || revision < 1 {
		return nil, ErrInvalidInput
	}
	if err := workflowAuthorize(ctx, tx, user, auth.ExecuteMigration, databaseID); err != nil {
		return nil, err
	}
	request, err := lockRequest(ctx, tx, databaseID, requestID)
	if err != nil {
		return nil, err
	}
	return checkLockedRequestApproval(ctx, tx, request, revision)
}

// Caller sudah memeriksa izin dan mengunci migration lalu request.
func checkLockedRequestApproval(ctx context.Context, tx *sql.Tx, request *MigrationRequest, revision int) (*MigrationRequest, error) {
	if request.Revision != revision || request.Snapshot.Revision != revision ||
		request.Snapshot.MigrationID != request.MigrationID || request.Snapshot.DatabaseID != request.DatabaseID {
		return nil, ErrConflict
	}
	var complete bool
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM migration_approvals WHERE request_id = $1) AND
		NOT EXISTS (SELECT 1 FROM migration_approvals WHERE request_id = $1 AND approved_by IS NULL)`, request.ID).Scan(&complete)
	if err != nil {
		return nil, err
	}
	if request.ReviewStatus != "approved" || !complete {
		return nil, ErrApprovalIncomplete
	}
	return request, nil
}
