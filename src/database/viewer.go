package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Error ini bisa dipetakan menjadi respons HTTP oleh handler nanti
var (
	ErrInvalidIdentifier = errors.New("nama schema atau tabel tidak valid")
	ErrSchemaNotFound    = errors.New("schema tidak ditemukan atau tidak dapat diakses")
	ErrTableNotFound     = errors.New("tabel tidak ditemukan atau tidak dapat diakses")
)

type Column struct {
	Name     string
	DataType string
	Nullable bool
}

// GetSchemas mengambil schema pengguna yang terlihat oleh akun PostgreSQL
func GetSchemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT schema_name
		FROM information_schema.schemata
		WHERE schema_name <> 'information_schema'
		  AND LEFT(schema_name, 3) <> 'pg_'
		ORDER BY schema_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schemas := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		schemas = append(schemas, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schemas, nil
}

// GetTables mengambil tabel biasa dan tabel partitioned, bukan view
func GetTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	if err := checkSchema(ctx, db, schema); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tables, nil
}

// GetColumns mengambil tipe PostgreSQL lengkap, misalnya numeric(10,2)
func GetColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	if err := checkTable(ctx, db, schema, table); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT c.column_name,
		       pg_catalog.format_type(a.atttypid, a.atttypmod),
		       c.is_nullable = 'YES'
		FROM information_schema.columns AS c
		JOIN pg_catalog.pg_namespace AS n ON n.nspname = c.table_schema
		JOIN pg_catalog.pg_class AS t
		  ON t.relnamespace = n.oid AND t.relname = c.table_name
		JOIN pg_catalog.pg_attribute AS a
		  ON a.attrelid = t.oid AND a.attnum = c.ordinal_position
		WHERE c.table_schema = $1 AND c.table_name = $2
		ORDER BY c.ordinal_position
	`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := []Column{}
	for rows.Next() {
		var column Column
		if err := rows.Scan(&column.Name, &column.DataType, &column.Nullable); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func checkSchema(ctx context.Context, db *sql.DB, schema string) error {
	if schema == "" || strings.ContainsRune(schema, '\x00') {
		return ErrInvalidIdentifier
	}
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.schemata WHERE schema_name = $1
		)
	`, schema).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSchemaNotFound
	}
	return nil
}

func checkTable(ctx context.Context, db *sql.DB, schema, table string) error {
	if table == "" || strings.ContainsRune(table, '\x00') {
		return ErrInvalidIdentifier
	}
	if err := checkSchema(ctx, db, schema); err != nil {
		return err
	}
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = $1 AND table_name = $2 AND table_type = 'BASE TABLE'
		)
	`, schema, table).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrTableNotFound
	}
	return nil
}
