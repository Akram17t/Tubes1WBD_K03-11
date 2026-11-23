package admin

import (
	"context"
	"database/sql"
	"errors"
)

type DatabaseGrant struct {
	DatabaseID   int64
	DatabaseName string
	AccessLevel  string
	IsActive     bool
}

type AccessDatabase struct {
	ID   int64
	Name string
}

type databaseAccessRepository struct{ db groupDB }

const grantQuery = `SELECT dc.id, dc.name, gda.access_level, dc.is_active
	FROM group_database_access gda JOIN database_connections dc ON dc.id = gda.database_id
	WHERE gda.group_id = $1`

// grants mengambil semua grant group untuk halaman HTML M1.
func (r databaseAccessRepository) grants(ctx context.Context, groupID int64) ([]DatabaseGrant, error) {
	return r.queryGrants(ctx, grantQuery+` ORDER BY dc.name, dc.id`, groupID)
}

// grantsPage mengambil grant dengan pagination di database.
func (r databaseAccessRepository) grantsPage(ctx context.Context, groupID int64, limit, offset int) ([]DatabaseGrant, error) {
	return r.queryGrants(ctx, grantQuery+` ORDER BY dc.name, dc.id LIMIT $2 OFFSET $3`, groupID, limit, offset)
}

// grant mencari grant berdasarkan pasangan group dan database pada URL.
func (r databaseAccessRepository) grant(ctx context.Context, groupID, databaseID int64) (DatabaseGrant, error) {
	return scanGrant(r.db.QueryRowContext(ctx, grantQuery+` AND gda.database_id = $2`, groupID, databaseID))
}

// queryGrants memetakan hasil query grant dengan format kolom yang sama.
func (r databaseAccessRepository) queryGrants(ctx context.Context, query string, args ...any) ([]DatabaseGrant, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := make([]DatabaseGrant, 0)
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

// scanGrant membedakan grant yang tidak ada dari kegagalan membaca kolom.
func scanGrant(row interface{ Scan(...any) error }) (DatabaseGrant, error) {
	var grant DatabaseGrant
	err := row.Scan(&grant.DatabaseID, &grant.DatabaseName, &grant.AccessLevel, &grant.IsActive)
	if errors.Is(err, sql.ErrNoRows) {
		err = errDatabaseAccessNotFound
	}
	return grant, err
}

// databases mengambil koneksi aktif yang dapat dipilih pada form grant M1.
func (r databaseAccessRepository) databases(ctx context.Context) ([]AccessDatabase, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM database_connections WHERE is_active ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	databases := make([]AccessDatabase, 0)
	for rows.Next() {
		var database AccessDatabase
		if err := rows.Scan(&database.ID, &database.Name); err != nil {
			return nil, err
		}
		databases = append(databases, database)
	}
	return databases, rows.Err()
}

// lockDatabase mencegah penghapusan atau deaktivasi koneksi mendahului validasi grant.
func (r databaseAccessRepository) lockDatabase(ctx context.Context, id int64, requireActive bool) error {
	var active bool
	err := r.db.QueryRowContext(ctx, `SELECT is_active FROM database_connections WHERE id = $1 FOR SHARE`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return errAccessDatabaseNotFound
	}
	if err != nil {
		return err
	}
	if requireActive && !active {
		return errAccessDatabaseInactive
	}
	return nil
}

// currentLevel membaca level grant sambil mengunci perubahan grant yang sama.
func (r databaseAccessRepository) currentLevel(ctx context.Context, groupID, databaseID int64) (string, error) {
	var level string
	err := r.db.QueryRowContext(ctx, `
		SELECT access_level FROM group_database_access
		WHERE group_id = $1 AND database_id = $2 FOR UPDATE`, groupID, databaseID).Scan(&level)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return level, err
}

// save membuat atau memperbarui grant tanpa menggandakan pasangan group dan database.
func (r databaseAccessRepository) save(ctx context.Context, groupID, databaseID int64, level string, grantedBy int64) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO group_database_access (group_id, database_id, access_level, granted_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id, database_id) DO UPDATE
		SET access_level = EXCLUDED.access_level, granted_by = EXCLUDED.granted_by, updated_at = now()`,
		groupID, databaseID, level, grantedBy)
	return err
}

// revoke mencabut satu grant dan menolak pasangan yang tidak ditemukan.
func (r databaseAccessRepository) revoke(ctx context.Context, groupID, databaseID int64) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM group_database_access WHERE group_id = $1 AND database_id = $2`, groupID, databaseID)
	return groupWriteResult(result, err, errDatabaseAccessNotFound)
}

// memberIDs mengambil ID anggota yang session atau privilege-nya perlu diperbarui.
func (r databaseAccessRepository) memberIDs(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT user_id FROM user_groups WHERE group_id = $1 ORDER BY user_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
