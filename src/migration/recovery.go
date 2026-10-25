package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tubes-wbd/src/database"
)

// RecoverRunning memulihkan eksekusi terputus dan melewati database yang masih dikunci
func RecoverRunning(ctx context.Context, db *sql.DB, targets database.TargetProvider) error {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT m.database_id
		FROM migration_executions e JOIN migrations m ON m.id = e.migration_id
		WHERE e.status = 'running' ORDER BY m.database_id
	`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := recoverDatabase(ctx, db, targets, id); err != nil {
			failures = append(failures, fmt.Errorf("database %d: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

func recoverDatabase(ctx context.Context, db *sql.DB, targets database.TargetProvider, id int64) error {
	platform, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer discardConnection(platform)
	var locked bool
	if err := platform.QueryRowContext(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, id).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	pool, err := database.TargetConnection(ctx, db, targets, id)
	if err != nil {
		return err
	}
	target, err := pool.Conn(ctx)
	if err != nil {
		return ErrTargetUnavailable
	}
	defer discardConnection(target)
	if err := target.QueryRowContext(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, id).Scan(&locked); err != nil {
		return ErrTargetUnavailable
	}
	if !locked {
		return nil
	}
	return reconcileExecutions(ctx, platform, target, id)
}
