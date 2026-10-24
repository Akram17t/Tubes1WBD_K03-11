package database

import (
	"context"
	"database/sql"

	"tubes-wbd/src/auth"
)

type Database struct {
	ID   int64
	Name string
}

// GetDatabases mengambil nama database aktif yang boleh diakses user
func GetDatabases(ctx context.Context, db *sql.DB, user *auth.User) ([]Database, error) {
	ids, err := auth.AccessibleDatabaseIDs(ctx, db, user)
	if err != nil {
		return nil, err
	}
	databases := []Database{}
	if len(ids) == 0 {
		return databases, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, name FROM database_connections
		WHERE is_active AND id = ANY($1::bigint[])
		ORDER BY name, id
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var database Database
		if err := rows.Scan(&database.ID, &database.Name); err != nil {
			return nil, err
		}
		databases = append(databases, database)
	}
	return databases, rows.Err()
}
