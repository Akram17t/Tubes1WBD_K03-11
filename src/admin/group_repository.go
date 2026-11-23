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

const groupColumns = `id, name, COALESCE(description, ''), leader_user_id, is_archived`

// list mengambil group yang belum diarsipkan beserta leader-nya.
func (r groupRepository) list(ctx context.Context) ([]Group, error) {
	return r.queryGroups(ctx, `SELECT `+groupColumns+` FROM groups WHERE NOT is_archived ORDER BY name, id`)
}

// listPage mengambil group aktif dengan pagination langsung pada database.
func (r groupRepository) listPage(ctx context.Context, limit, offset int) ([]Group, error) {
	return r.queryGroups(ctx, `SELECT `+groupColumns+`
		FROM groups WHERE NOT is_archived ORDER BY name, id LIMIT $1 OFFSET $2`, limit, offset)
}

// queryGroups membaca hasil query group tanpa mengulang pemetaan kolom.
func (r groupRepository) queryGroups(ctx context.Context, query string, args ...any) ([]Group, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]Group, 0)
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// find mencari satu group aktif atau mengembalikan error tidak ditemukan.
func (r groupRepository) find(ctx context.Context, id int64) (Group, error) {
	return scanGroup(r.db.QueryRowContext(ctx, `SELECT `+groupColumns+` FROM groups WHERE id = $1 AND NOT is_archived`, id))
}

// scanGroup memetakan kolom group dan membedakan row kosong dari kegagalan database.
func scanGroup(row interface{ Scan(...any) error }) (Group, error) {
	var group Group
	err := row.Scan(&group.ID, &group.Name, &group.Description, &group.LeaderUserID, &group.IsArchived)
	if errors.Is(err, sql.ErrNoRows) {
		err = errGroupNotFound
	}
	return group, err
}

// create menyimpan group baru dengan deskripsi opsional.
func (r groupRepository) create(ctx context.Context, name, description string) (Group, error) {
	group, err := scanGroup(r.db.QueryRowContext(ctx, `
		INSERT INTO groups (name, description) VALUES ($1, NULLIF($2, '')) RETURNING `+groupColumns, name, description))
	return group, groupDatabaseError(err)
}

// update mengganti nama dan deskripsi group yang belum diarsipkan.
func (r groupRepository) update(ctx context.Context, id int64, name, description string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE groups SET name = $1, description = NULLIF($2, ''), updated_at = now()
		WHERE id = $3 AND NOT is_archived`, name, description, id)
	return groupWriteResult(result, err, errGroupNotFound)
}

// members mengambil anggota group beserta status akun mereka.
func (r groupRepository) members(ctx context.Context, id int64) ([]GroupMember, error) {
	return r.users(ctx, `
		SELECT u.id, u.username, u.display_name, u.is_active
		FROM users u JOIN user_groups ug ON ug.user_id = u.id
		WHERE ug.group_id = $1 ORDER BY u.username, u.id`, id)
}

// membersPage mengambil satu halaman anggota dengan urutan username dan ID yang stabil.
func (r groupRepository) membersPage(ctx context.Context, id int64, limit, offset int) ([]GroupMember, error) {
	return r.users(ctx, `
		SELECT u.id, u.username, u.display_name, u.is_active
		FROM users u JOIN user_groups ug ON ug.user_id = u.id
		WHERE ug.group_id = $1 ORDER BY u.username, u.id LIMIT $2 OFFSET $3`, id, limit, offset)
}

// availableUsers mengambil user yang belum menjadi anggota group.
func (r groupRepository) availableUsers(ctx context.Context, id int64) ([]GroupMember, error) {
	return r.users(ctx, `
		SELECT u.id, u.username, u.display_name, u.is_active FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM user_groups ug WHERE ug.user_id = u.id AND ug.group_id = $1)
		ORDER BY u.username, u.id`, id)
}

// users membaca hasil query anggota atau calon anggota ke bentuk GroupMember.
func (r groupRepository) users(ctx context.Context, query string, args ...any) ([]GroupMember, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
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

// lock mengunci group aktif sebelum perubahan anggota, leader, atau pengarsipan.
func (r groupRepository) lock(ctx context.Context, id int64) error {
	err := r.db.QueryRowContext(ctx, `SELECT id FROM groups WHERE id = $1 AND NOT is_archived FOR UPDATE`, id).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return errGroupNotFound
	}
	return err
}

// lockUser mengunci akun agar perubahan keanggotaan tidak berbenturan dengan perubahan user.
func (r groupRepository) lockUser(ctx context.Context, id int64) error {
	err := r.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return errGroupUserNotFound
	}
	return err
}

// archive menyimpan record group sambil mengosongkan leader, anggota, dan grant aksesnya.
func (r groupRepository) archive(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE groups SET is_archived = true, leader_user_id = NULL, updated_at = now()
		WHERE id = $1 AND NOT is_archived`, id)
	if err := groupWriteResult(result, err, errGroupNotFound); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM user_groups WHERE group_id = $1`, id); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM group_database_access WHERE group_id = $1`, id)
	return err
}

// setLeader menetapkan anggota aktif sebagai leader atau mengosongkannya jika nil.
func (r groupRepository) setLeader(ctx context.Context, groupID int64, leaderID *int64) error {
	if leaderID != nil {
		var active bool
		err := r.db.QueryRowContext(ctx, `
			SELECT u.is_active FROM users u
			JOIN user_groups ug ON ug.user_id = u.id
			WHERE ug.group_id = $1 AND u.id = $2 FOR UPDATE OF u`, groupID, *leaderID).Scan(&active)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
			return errInvalidGroupLeader
		}
		if err != nil {
			return err
		}
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE groups SET leader_user_id = $2, updated_at = now()
		WHERE id = $1 AND NOT is_archived`, groupID, leaderID)
	return groupWriteResult(result, err, errGroupNotFound)
}

// addMember menambahkan relasi keanggotaan dan menolak anggota duplikat.
func (r groupRepository) addMember(ctx context.Context, groupID, userID int64) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO user_groups (group_id, user_id) VALUES ($1, $2)`, groupID, userID)
	return groupDatabaseError(err)
}

// removeMember menghapus keanggotaan dan foreign key ikut mengosongkan leader bila diperlukan.
func (r groupRepository) removeMember(ctx context.Context, groupID, userID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM user_groups WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return groupWriteResult(result, err, errGroupMemberNotFound)
}

// groupWriteResult membedakan kegagalan query dari target perubahan yang tidak ditemukan.
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

// groupDatabaseError menerjemahkan pelanggaran constraint yang dikenal menjadi error group.
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
