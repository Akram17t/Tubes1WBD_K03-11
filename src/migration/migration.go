package migration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"tubes-wbd/src/auth"
)

var (
	ErrInvalidInput = errors.New("ID, judul, atau SQL migration tidak valid")
	ErrNotFound     = errors.New("migration tidak ditemukan")
	ErrConflict     = errors.New("migration hanya bisa diubah saat draft atau failed")
)

type Migration struct {
	ID          int64
	DatabaseID  int64
	Version     sql.NullInt64
	Title       string
	Description sql.NullString
	SQLContent  string
	Status      string
	CreatedBy   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const migrationColumns = `id, database_id, version, title, description,
	sql_content, status, created_by, created_at, updated_at`

// CreateMigration menyimpan draft SQL pada database platform
func CreateMigration(ctx context.Context, db *sql.DB, user *auth.User, dbID int64, title, description, sqlContent string) (*Migration, error) {
	title = strings.TrimSpace(title)
	if dbID < 1 || !validDraft(title, sqlContent) {
		return nil, ErrInvalidInput
	}
	if err := auth.Authorize(ctx, db, user, auth.CreateMigration, dbID); err != nil {
		return nil, err
	}

	return scanMigration(db.QueryRowContext(ctx, `
		INSERT INTO migrations (database_id, title, description, sql_content, created_by)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5)
		RETURNING `+migrationColumns,
		dbID, title, description, sqlContent, user.ID,
	))
}

// GetMigrations mengambil daftar migration pada satu database
func GetMigrations(ctx context.Context, db *sql.DB, user *auth.User, dbID int64) ([]Migration, error) {
	if dbID < 1 {
		return nil, ErrInvalidInput
	}
	if err := auth.Authorize(ctx, db, user, auth.ViewMigration, dbID); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT `+migrationColumns+`
		FROM migrations WHERE database_id = $1
		ORDER BY created_at DESC, id DESC
	`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	migrations := []Migration{}
	for rows.Next() {
		migration, err := scanMigration(rows)
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, *migration)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return migrations, nil
}

// GetMigration mengambil detail migration yang dimiliki database tersebut
func GetMigration(ctx context.Context, db *sql.DB, user *auth.User, dbID, migrationID int64) (*Migration, error) {
	if dbID < 1 || migrationID < 1 {
		return nil, ErrInvalidInput
	}
	if err := auth.Authorize(ctx, db, user, auth.ViewMigration, dbID); err != nil {
		return nil, err
	}

	return scanMigration(db.QueryRowContext(ctx, `
		SELECT `+migrationColumns+`
		FROM migrations WHERE database_id = $1 AND id = $2
	`, dbID, migrationID))
}

// UpdateMigration mengubah draft atau migration gagal tanpa mengubah riwayat eksekusi
func UpdateMigration(ctx context.Context, db *sql.DB, user *auth.User, dbID, migrationID int64, title, description, sqlContent string) (*Migration, error) {
	title = strings.TrimSpace(title)
	if dbID < 1 || migrationID < 1 || !validDraft(title, sqlContent) {
		return nil, ErrInvalidInput
	}
	if err := auth.Authorize(ctx, db, user, auth.EditMigration, dbID); err != nil {
		return nil, err
	}

	migration, err := scanMigration(db.QueryRowContext(ctx, `
		UPDATE migrations
		SET title = $3, description = NULLIF($4, ''), sql_content = $5, updated_at = now()
		WHERE database_id = $1 AND id = $2 AND status IN ('draft', 'failed')
		RETURNING `+migrationColumns,
		dbID, migrationID, title, description, sqlContent,
	))
	if !errors.Is(err, ErrNotFound) {
		return migration, err
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM migrations WHERE database_id = $1 AND id = $2)
	`, dbID, migrationID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return nil, ErrConflict
}

func validDraft(title, sqlContent string) bool {
	return title != "" && utf8.RuneCountInString(title) <= 200 && strings.TrimSpace(sqlContent) != ""
}

func scanMigration(row interface{ Scan(...any) error }) (*Migration, error) {
	var migration Migration
	err := row.Scan(
		&migration.ID, &migration.DatabaseID, &migration.Version, &migration.Title,
		&migration.Description, &migration.SQLContent, &migration.Status,
		&migration.CreatedBy, &migration.CreatedAt, &migration.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &migration, nil
}
