package database

import (
	"context"
	"database/sql"
	"errors"

	"tubes-wbd/src/auth"
)

type Database struct {
	ID   int64
	Name string
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
	return &database, nil
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
