package admin

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type groupDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type groupRepository struct{ db groupDB }

func (r groupRepository) list(ctx context.Context) ([]Group, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(description, '') FROM groups ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]Group, 0)
	for rows.Next() {
		var group Group
		if err := rows.Scan(&group.ID, &group.Name, &group.Description); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (r groupRepository) find(ctx context.Context, id int64) (Group, error) {
	var group Group
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(description, '') FROM groups WHERE id = $1`, id).
		Scan(&group.ID, &group.Name, &group.Description)
	if errors.Is(err, sql.ErrNoRows) {
		err = errGroupNotFound
	}
	return group, err
}

func (r groupRepository) create(ctx context.Context, name, description string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO groups (name, description) VALUES ($1, NULLIF($2, ''))`, name, description)
	return groupDatabaseError(err)
}

func (r groupRepository) update(ctx context.Context, id int64, name, description string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE groups SET name = $1, description = NULLIF($2, ''), updated_at = now()
		WHERE id = $3`, name, description, id)
	return groupWriteResult(result, err, errGroupNotFound)
}

func (r groupRepository) members(ctx context.Context, id int64) ([]GroupMember, error) {
	return r.users(ctx, `
		SELECT u.id, u.username, u.display_name, u.is_active
		FROM users u JOIN user_groups ug ON ug.user_id = u.id
		WHERE ug.group_id = $1 ORDER BY u.username, u.id`, id)
}

func (r groupRepository) availableUsers(ctx context.Context, id int64) ([]GroupMember, error) {
	return r.users(ctx, `
		SELECT u.id, u.username, u.display_name, u.is_active FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM user_groups ug WHERE ug.user_id = u.id AND ug.group_id = $1)
		ORDER BY u.username, u.id`, id)
}

func (r groupRepository) users(ctx context.Context, query string, id int64) ([]GroupMember, error) {
	rows, err := r.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]GroupMember, 0)
	for rows.Next() {
		var user GroupMember
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.IsActive); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// Lock the parent before every membership change or group deletion.
func (r groupRepository) lock(ctx context.Context, id int64) error {
	err := r.db.QueryRowContext(ctx, `SELECT id FROM groups WHERE id = $1 FOR UPDATE`, id).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return errGroupNotFound
	}
	return err
}

func (r groupRepository) lockUser(ctx context.Context, id int64) error {
	err := r.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return errGroupUserNotFound
	}
	return err
}

func (r groupRepository) deleteGroup(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, id)
	return groupWriteResult(result, err, errGroupNotFound)
}

func (r groupRepository) addMember(ctx context.Context, groupID, userID int64) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO user_groups (group_id, user_id) VALUES ($1, $2)`, groupID, userID)
	return groupDatabaseError(err)
}

func (r groupRepository) removeMember(ctx context.Context, groupID, userID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM user_groups WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return groupWriteResult(result, err, errGroupMemberNotFound)
}

func groupWriteResult(result sql.Result, err error, missing error) error {
	if err != nil {
		return groupDatabaseError(err)
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return missing
	}
	return err
}

func groupDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "groups_name_key":
			return errGroupNameExists
		case "user_groups_pkey":
			return errGroupMemberExists
		}
	}
	return err
}
