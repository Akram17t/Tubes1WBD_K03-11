package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const MaxPageSize = 500

// Error ini bisa dipetakan menjadi respons HTTP oleh handler nanti
var (
	ErrInvalidIdentifier = errors.New("nama schema atau tabel tidak valid")
	ErrSchemaNotFound    = errors.New("schema tidak ditemukan atau tidak dapat diakses")
	ErrTableNotFound     = errors.New("tabel tidak ditemukan atau tidak dapat diakses")
	ErrInvalidPagination = errors.New("limit di luar batas atau offset negatif")
)

type Column struct {
	Name     string
	DataType string
	Nullable bool
}

type Constraint struct {
	Name       string
	Type       string
	Definition string
}

type Index struct {
	Name       string
	Definition string
}

type TableData struct {
	Columns []string
	Rows    [][]any
	Limit   int
	Offset  int
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
		  AND NOT (table_schema = 'public' AND table_name = '_dbplatform_migrations')
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

// GetConstraints mengambil nama, jenis, dan definisi SQL constraint tabel
func GetConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	if err := checkTable(ctx, db, schema, table); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT c.conname,
		       CASE c.contype
		         WHEN 'p' THEN 'PRIMARY KEY'
		         WHEN 'f' THEN 'FOREIGN KEY'
		         WHEN 'u' THEN 'UNIQUE'
		         WHEN 'c' THEN 'CHECK'
		         WHEN 'x' THEN 'EXCLUDE'
		         WHEN 'n' THEN 'NOT NULL'
		         ELSE c.contype::text
		       END,
		       pg_catalog.pg_get_constraintdef(c.oid)
		FROM pg_catalog.pg_constraint AS c
		JOIN pg_catalog.pg_class AS t ON t.oid = c.conrelid
		JOIN pg_catalog.pg_namespace AS n ON n.oid = t.relnamespace
		WHERE n.nspname = $1 AND t.relname = $2
		ORDER BY c.conname
	`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	constraints := []Constraint{}
	for rows.Next() {
		var constraint Constraint
		if err := rows.Scan(&constraint.Name, &constraint.Type, &constraint.Definition); err != nil {
			return nil, err
		}
		constraints = append(constraints, constraint)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return constraints, nil
}

// GetIndexes mengambil nama index dan definisi CREATE INDEX-nya
func GetIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	if err := checkTable(ctx, db, schema, table); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT indexname, indexdef
		FROM pg_catalog.pg_indexes
		WHERE schemaname = $1 AND tablename = $2
		ORDER BY indexname
	`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	indexes := []Index{}
	for rows.Next() {
		var index Index
		if err := rows.Scan(&index.Name, &index.Definition); err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return indexes, nil
}

// GetTableData mengambil isi tabel dengan pagination
func GetTableData(ctx context.Context, db *sql.DB, schema, table string, limit, offset int) (*TableData, error) {
	if limit < 1 || limit > MaxPageSize || offset < 0 {
		return nil, ErrInvalidPagination
	}
	if err := checkTable(ctx, db, schema, table); err != nil {
		return nil, err
	}

	query := `SELECT * FROM ` + quoteIdentifier(schema) + `.` + quoteIdentifier(table) +
		` ORDER BY tableoid, ctid LIMIT $1 OFFSET $2`
	rows, err := db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	data := &TableData{Columns: columns, Rows: [][]any{}, Limit: limit, Offset: offset}

	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		data.Rows = append(data.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return data, nil
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
			  AND NOT (table_schema = 'public' AND table_name = '_dbplatform_migrations')
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

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
