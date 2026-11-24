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
	// State job dibaca dari schema queue; blocker tetap terlihat meskipun worker berhenti.
	ActiveJobID   *int64  `json:"active_job_id,string"`
	BlockingJobID *int64  `json:"blocking_job_id,string"`
	BlockReason   *string `json:"block_reason"`
}

// Join memakai indeks status job; pembacaan viewer tidak memanggil atau mengaktifkan worker.
const databaseJobJoins = ` LEFT JOIN LATERAL (
	SELECT id FROM migration_jobs WHERE database_id=d.id AND status='running' LIMIT 1
) active_job ON true LEFT JOIN LATERAL (
	SELECT id,status FROM migration_jobs WHERE database_id=d.id
	AND status IN ('partial','partially_reverted','outcome_unknown') ORDER BY id LIMIT 1
) blocker ON true `

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
		SELECT d.id,d.name,active_job.id,blocker.id,blocker.status FROM database_connections d
	`+databaseJobJoins+` WHERE d.id = $1 AND d.is_active
	`, dbID).Scan(&database.ID, &database.Name, &database.ActiveJobID, &database.BlockingJobID, &database.BlockReason)
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

// getDatabasesPage memfilter akses dan pagination dalam satu query tanpa memuat seluruh ID terlebih dahulu.
func getDatabasesPage(ctx context.Context, db *sql.DB, user *auth.User, limit, offset int) ([]Database, error) {
	if user == nil {
		return nil, auth.ErrForbidden
	}
	databases := []Database{}
	rows, err := db.QueryContext(ctx, `
		SELECT d.id, d.name,
			CASE MAX(CASE a.access_level WHEN 'edit' THEN 2 ELSE 1 END) WHEN 2 THEN 'edit' ELSE 'view' END,
			active_job.id,blocker.id,blocker.status
		FROM database_connections d
		JOIN group_database_access a ON a.database_id = d.id
		JOIN groups g ON g.id = a.group_id AND NOT g.is_archived
		JOIN user_groups ug ON ug.group_id = g.id AND ug.user_id = $1
	`+databaseJobJoins+` WHERE d.is_active
		GROUP BY d.id,active_job.id,blocker.id,blocker.status ORDER BY d.name, d.id LIMIT NULLIF($2,0) OFFSET $3
	`, user.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var database Database
		if err := rows.Scan(&database.ID, &database.Name, &database.AccessLevel, &database.ActiveJobID, &database.BlockingJobID, &database.BlockReason); err != nil {
			return nil, err
		}
		databases = append(databases, database)
	}
	return databases, rows.Err()
}
