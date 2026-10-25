package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"tubes-wbd/src/auth"
)

var (
	ErrInvalidInput      = errors.New("ID, judul, atau SQL migration tidak valid")
	ErrNotFound          = errors.New("migration tidak ditemukan")
	ErrConflict          = errors.New("status migration tidak mengizinkan operasi ini")
	ErrExecutionFailed   = errors.New("eksekusi SQL migration gagal")
	ErrOutcomeUnknown    = errors.New("hasil eksekusi belum pasti; status akan dipulihkan sebelum eksekusi berikutnya")
	ErrTargetUnavailable = errors.New("database target tidak dapat dihubungi atau diperiksa")
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

type Execution struct {
	ID           int64
	MigrationID  int64
	ExecutedBy   int64
	SQLSnapshot  string
	Status       string
	StartedAt    time.Time
	FinishedAt   sql.NullTime
	ErrorMessage sql.NullString
}

const migrationColumns = `id, database_id, version, title, description,
	sql_content, status, created_by, created_at, updated_at`

const executionColumns = `id, migration_id, executed_by, sql_snapshot,
	status, started_at, finished_at, error_message`

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

// GetExecutions mengambil riwayat percobaan eksekusi pada satu migration
func GetExecutions(ctx context.Context, db *sql.DB, user *auth.User, dbID, migrationID int64) ([]Execution, error) {
	if _, err := GetMigration(ctx, db, user, dbID, migrationID); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT `+executionColumns+` FROM migration_executions
		WHERE migration_id = $1 ORDER BY started_at DESC, id DESC
	`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	executions := []Execution{}
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		executions = append(executions, *execution)
	}
	return executions, rows.Err()
}

// GetExecution mengambil detail percobaan milik migration dan database tersebut
func GetExecution(ctx context.Context, db *sql.DB, user *auth.User, dbID, migrationID, executionID int64) (*Execution, error) {
	if executionID < 1 {
		return nil, ErrInvalidInput
	}
	if _, err := GetMigration(ctx, db, user, dbID, migrationID); err != nil {
		return nil, err
	}
	execution, err := scanExecution(db.QueryRowContext(ctx, `
		SELECT `+executionColumns+` FROM migration_executions
		WHERE migration_id = $1 AND id = $2
	`, migrationID, executionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return execution, err
}

// ExecuteMigration menjalankan SQL pada target dan mencatat hasil pada database platform
func ExecuteMigration(ctx context.Context, db, targetDB *sql.DB, user *auth.User, dbID, migrationID int64) (*Execution, error) {
	if dbID < 1 || migrationID < 1 || db == nil || targetDB == nil || db == targetDB {
		return nil, ErrInvalidInput
	}
	if err := auth.Authorize(ctx, db, user, auth.ExecuteMigration, dbID); err != nil {
		return nil, err
	}

	platform, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer discardConnection(platform)
	if _, err := platform.ExecContext(ctx, `SELECT pg_catalog.pg_advisory_lock($1)`, dbID); err != nil {
		return nil, err
	}

	migration, err := scanMigration(platform.QueryRowContext(ctx, `
		SELECT `+migrationColumns+` FROM migrations WHERE database_id = $1 AND id = $2
	`, dbID, migrationID))
	if err != nil {
		return nil, err
	}
	if migration.Status == "success" {
		return nil, ErrConflict
	}

	target, err := targetDB.Conn(ctx)
	if err != nil {
		return nil, ErrTargetUnavailable
	}
	defer discardConnection(target)
	var locked bool
	if err := target.QueryRowContext(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, dbID).Scan(&locked); err != nil || !locked {
		return nil, ErrTargetUnavailable
	}
	if err := auth.Authorize(ctx, platform, user, auth.ExecuteMigration, dbID); err != nil {
		return nil, err
	}
	if err := reconcileExecutions(ctx, platform, target, dbID); err != nil {
		return nil, err
	}

	var version int64
	if err := platform.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM migrations WHERE database_id = $1
	`, dbID).Scan(&version); err != nil {
		return nil, err
	}
	execution, err := startExecution(ctx, platform, user.ID, dbID, migrationID)
	if err != nil {
		return execution, err
	}

	commitAttempted, err := executeTargetSQL(ctx, target, execution, version)
	if err != nil {
		if commitAttempted {
			return execution, ErrOutcomeUnknown
		}
		failure := error(ErrExecutionFailed)
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) {
			failure = fmt.Errorf("%w (SQLSTATE %s)", ErrExecutionFailed, pgError.Code)
		}
		failed, recordErr := finishExecution(ctx, platform, dbID, execution.ID, migrationID, 0, "failed", failure.Error())
		if recordErr != nil {
			return execution, ErrOutcomeUnknown
		}
		return failed, failure
	}

	finished, err := finishExecution(ctx, platform, dbID, execution.ID, migrationID, version, "success", "")
	if err != nil {
		return execution, ErrOutcomeUnknown
	}
	return finished, nil
}

func startExecution(ctx context.Context, platform *sql.Conn, userID, dbID, migrationID int64) (*Execution, error) {
	tx, err := platform.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var snapshot string
	err = tx.QueryRowContext(ctx, `
		UPDATE migrations SET status = 'running', updated_at = now()
		WHERE database_id = $1 AND id = $2 AND status IN ('draft', 'failed')
		RETURNING sql_content
	`, dbID, migrationID).Scan(&snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	execution, err := scanExecution(tx.QueryRowContext(ctx, `
		INSERT INTO migration_executions (migration_id, executed_by, sql_snapshot)
		VALUES ($1, $2, $3) RETURNING `+executionColumns,
		migrationID, userID, snapshot,
	))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return execution, ErrOutcomeUnknown
	}
	return execution, nil
}

func executeTargetSQL(ctx context.Context, target *sql.Conn, execution *Execution, version int64) (commitAttempted bool, err error) {
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS public._dbplatform_migrations (
			execution_id BIGINT PRIMARY KEY,
			migration_id BIGINT NOT NULL,
			version BIGINT NOT NULL UNIQUE CHECK (version > 0),
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return false, err
	}
	// Blok DO menolak COMMIT/ROLLBACK dalam SQL agar transaksi tidak terpecah
	var command string
	if err := tx.QueryRowContext(ctx, `
		SELECT pg_catalog.format('DO %L', pg_catalog.format('BEGIN EXECUTE %L; END;', $1::text))
	`, execution.SQLSnapshot).Scan(&command); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, command); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public._dbplatform_migrations (execution_id, migration_id, version)
		VALUES ($1, $2, $3)
	`, execution.ID, execution.MigrationID, version); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func finishExecution(ctx context.Context, platform *sql.Conn, dbID, executionID, migrationID, version int64, status, message string) (*Execution, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := platform.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = $3, version = NULLIF($4::bigint, 0), updated_at = now()
		WHERE database_id = $1 AND id = $2 AND status = 'running'
	`, dbID, migrationID, status, version)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrConflict
	}
	execution, err := scanExecution(tx.QueryRowContext(ctx, `
		UPDATE migration_executions
		SET status = $2, finished_at = now(), error_message = NULLIF($3, '')
		WHERE id = $1 AND migration_id = $4 AND status = 'running'
		RETURNING `+executionColumns,
		executionID, status, message, migrationID,
	))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return execution, nil
}

func reconcileExecutions(ctx context.Context, platform, target *sql.Conn, dbID int64) error {
	rows, err := platform.QueryContext(ctx, `
		SELECT e.id, e.migration_id
		FROM migration_executions e JOIN migrations m ON m.id = e.migration_id
		WHERE m.database_id = $1 AND e.status = 'running'
		ORDER BY e.id
	`, dbID)
	if err != nil {
		return err
	}
	var pending []Execution
	for rows.Next() {
		var execution Execution
		if err := rows.Scan(&execution.ID, &execution.MigrationID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, execution)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(pending) == 0 {
		return err
	}

	var hasTracking bool
	if err := target.QueryRowContext(ctx, `SELECT pg_catalog.to_regclass('public._dbplatform_migrations') IS NOT NULL`).Scan(&hasTracking); err != nil {
		return ErrTargetUnavailable
	}
	for _, execution := range pending {
		var version int64
		status, message := "failed", "eksekusi sebelumnya terputus sebelum commit"
		if hasTracking {
			err := target.QueryRowContext(ctx, `
				SELECT version FROM public._dbplatform_migrations
				WHERE execution_id = $1 AND migration_id = $2
			`, execution.ID, execution.MigrationID).Scan(&version)
			if err == nil {
				status, message = "success", ""
			} else if !errors.Is(err, sql.ErrNoRows) {
				return ErrTargetUnavailable
			}
		}
		if _, err := finishExecution(ctx, platform, dbID, execution.ID, execution.MigrationID, version, status, message); err != nil {
			return err
		}
	}
	return nil
}

// Koneksi fisik ditutup agar advisory lock dan perubahan session ikut dibersihkan
func discardConnection(conn *sql.Conn) {
	conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
}

func scanExecution(row interface{ Scan(...any) error }) (*Execution, error) {
	var execution Execution
	err := row.Scan(
		&execution.ID, &execution.MigrationID, &execution.ExecutedBy,
		&execution.SQLSnapshot, &execution.Status, &execution.StartedAt,
		&execution.FinishedAt, &execution.ErrorMessage,
	)
	if err != nil {
		return nil, err
	}
	return &execution, nil
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
