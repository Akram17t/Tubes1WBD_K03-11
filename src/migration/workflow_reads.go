package migration

import (
	"context"
	"database/sql"
	"strconv"

	"tubes-wbd/src/auth"
)

// List mengambil satu halaman pengajuan database dan mendeteksi keberadaan halaman berikutnya.
func (s WorkflowService) List(ctx context.Context, user *auth.User, databaseID int64, limit, offset int) ([]MigrationRequest, bool, error) {
	if databaseID < 1 || limit < 1 || limit > 500 || offset < 0 {
		return nil, false, ErrInvalidInput
	}
	if err := workflowAuthorize(ctx, s.DB, user, auth.ViewMigration, databaseID); err != nil {
		return nil, false, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+requestColumns+`
		FROM migration_requests WHERE database_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, databaseID, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	requests := []MigrationRequest{}
	for rows.Next() {
		request, err := scanRequest(rows)
		if err != nil {
			return nil, false, err
		}
		requests = append(requests, *request)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(requests) > limit
	if hasMore {
		requests = requests[:limit]
	}
	return requests, hasMore, nil
}

// Get membaca snapshot, approval, job, dan aksi yang tersedia dari satu snapshot transaction.
func (s WorkflowService) Get(ctx context.Context, user *auth.User, databaseID, requestID int64) (*RequestDetail, error) {
	if databaseID < 1 || requestID < 1 {
		return nil, ErrInvalidInput
	}
	// Baca status, approval, dan job dari snapshot database yang sama.
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := workflowAuthorize(ctx, tx, user, auth.ViewMigration, databaseID); err != nil {
		return nil, err
	}
	request, err := scanRequest(tx.QueryRowContext(ctx, `SELECT `+requestColumns+`
		FROM migration_requests WHERE database_id = $1 AND id = $2`, databaseID, requestID))
	if err != nil {
		return nil, err
	}
	detail := &RequestDetail{MigrationRequest: *request, Approvals: []RequestApproval{}, PendingGroupIDs: []string{}, AllowedActions: []string{}}
	rows, err := tx.QueryContext(ctx, `
		SELECT a.group_id, a.group_name_snapshot, a.approved_by, a.approved_at,
			a.approved_by IS NULL AND (g.is_archived OR u.id IS NULL),
			a.approved_by IS NULL AND NOT g.is_archived AND u.id = $2
		FROM migration_approvals a JOIN groups g ON g.id = a.group_id
		LEFT JOIN users u ON u.id = g.leader_user_id AND u.is_active
			AND EXISTS (SELECT 1 FROM user_groups ug WHERE ug.group_id = g.id AND ug.user_id = u.id)
		WHERE a.request_id = $1 ORDER BY a.group_id`, requestID, user.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var approval RequestApproval
		var canApprove sql.NullBool
		if err := rows.Scan(&approval.GroupID, &approval.GroupName, &approval.ApprovedBy, &approval.ApprovedAt, &approval.WaitingForLeader, &canApprove); err != nil {
			rows.Close()
			return nil, err
		}
		approval.CanApprove = request.ReviewStatus == "pending_review" && canApprove.Bool
		if approval.ApprovedAt != nil {
			utc := approval.ApprovedAt.UTC()
			approval.ApprovedAt = &utc
		}
		detail.CanApprove = detail.CanApprove || approval.CanApprove
		if approval.ApprovedBy == nil {
			detail.PendingGroupIDs = append(detail.PendingGroupIDs, strconv.FormatInt(approval.GroupID, 10))
		}
		detail.Approvals = append(detail.Approvals, approval)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if s.FindJob == nil {
		return nil, ErrWorkflowUnavailable
	}
	detail.JobID, err = s.FindJob(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	level, err := auth.EffectiveLevel(ctx, tx, user.ID, databaseID)
	if err != nil {
		return nil, err
	}
	// Aksi tampilan diturunkan dari izin terkini dan tetap diperiksa ulang saat mutasi.
	if detail.CanApprove {
		detail.AllowedActions = append(detail.AllowedActions, "approve")
	}
	if request.RequestedBy == user.ID && level == auth.LevelEdit && detail.JobID == nil && request.ReviewStatus != "cancelled" {
		detail.AllowedActions = append(detail.AllowedActions, "cancel")
		if request.ReviewStatus == "draft" && s.Validate != nil {
			detail.AllowedActions = append(detail.AllowedActions, "submit")
		}
	}
	return detail, tx.Commit()
}
