package database

import (
	"context"
	"database/sql"
	"errors"

	"tubes-wbd/src/auth"
)

type Database struct {
	ID          int64  `json:"id,string"`
	Name        string `json:"name"`
	AccessLevel string `json:"access_level"`
	// shortcut: isi status job setelah schema queue Manuel tersedia.
	ActiveJobID   *int64  `json:"active_job_id,string"`
	BlockingJobID *int64  `json:"blocking_job_id,string"`
	BlockReason   *string `json:"block_reason"`
}

var (
	ErrInvalidDatabaseID = errors.New("ID database tidak valid")
	ErrDatabaseNotFound  = errors.New("database tidak ditemukan")
)

// GetDatabase mengambil ringkasan database yang boleh diakses user
func GetDatabase(ctx context.Context, db *sql.DB, user *auth.User, dbID int64) (*Database, error) {
	if dbID < 1 {
		return nil, ErrInvalidDatabaseID
	}
	if err := auth.Authorize(ctx, db, user, auth.ViewSchema, dbID); err != nil {
		return nil, err
	}
	var database Database
	err := db.QueryRowContext(ctx, `
		SELECT id, name FROM database_connections WHERE id = $1 AND is_active
	`, dbID).Scan(&database.ID, &database.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDatabaseNotFound
	}
	if err != nil {
		return nil, err
	}
	level, err := auth.EffectiveLevel(ctx, db, user.ID, dbID)
	if err != nil {
		return nil, err
	}
	if level == auth.LevelNone {
		return nil, auth.ErrForbidden
	}
	database.AccessLevel = "view"
	if level == auth.LevelEdit {
		database.AccessLevel = "edit"
	}
	return &database, nil
}

// GetDatabases mengambil nama database aktif yang boleh diakses user
func GetDatabases(ctx context.Context, db *sql.DB, user *auth.User) ([]Database, error) {
	return getDatabasesPage(ctx, db, user, 0, 0)
}

func getDatabasesPage(ctx context.Context, db *sql.DB, user *auth.User, limit, offset int) ([]Database, error) {
	ids, err := auth.AccessibleDatabaseIDs(ctx, db, user)
	if err != nil {
		return nil, err
	}
	databases := []Database{}
	if len(ids) == 0 {
		return databases, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT d.id, d.name,
			CASE MAX(CASE a.access_level WHEN 'edit' THEN 2 ELSE 1 END) WHEN 2 THEN 'edit' ELSE 'view' END
		FROM database_connections d
		JOIN group_database_access a ON a.database_id = d.id
		JOIN groups g ON g.id = a.group_id AND NOT g.is_archived
		JOIN user_groups ug ON ug.group_id = g.id AND ug.user_id = $2
		WHERE d.is_active AND d.id = ANY($1::bigint[])
		GROUP BY d.id ORDER BY d.name, d.id LIMIT NULLIF($3,0) OFFSET $4
	`, ids, user.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var database Database
		if err := rows.Scan(&database.ID, &database.Name, &database.AccessLevel); err != nil {
			return nil, err
		}
		databases = append(databases, database)
	}
	return databases, rows.Err()
}
