package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type connectionDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type connectionRepository struct{ db connectionDB }

const connectionColumns = `id,name,host,port,database_name,db_username,ssl_mode,is_active`

type storedConnection struct {
	Connection
	passwordEncrypted string
}

func (r connectionRepository) list(ctx context.Context) ([]Connection, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+connectionColumns+` FROM database_connections ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := make([]Connection, 0)
	for rows.Next() {
		var connection Connection
		if err := scanConnection(rows, &connection); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func (r connectionRepository) find(ctx context.Context, id int64) (Connection, error) {
	var connection Connection
	err := scanConnection(r.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM database_connections WHERE id=$1`, id), &connection)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrConnectionNotFound
	}
	return connection, err
}

func scanConnection(row interface{ Scan(...any) error }, connection *Connection) error {
	return row.Scan(&connection.ID, &connection.Name, &connection.Host, &connection.Port, &connection.DatabaseName, &connection.DBUsername, &connection.SSLMode, &connection.IsActive)
}

func (r connectionRepository) stored(ctx context.Context, id int64) (storedConnection, error) {
	var connection storedConnection
	err := r.db.QueryRowContext(ctx, `SELECT `+connectionColumns+`,password_encrypted FROM database_connections WHERE id=$1 FOR SHARE`, id).
		Scan(&connection.ID, &connection.Name, &connection.Host, &connection.Port, &connection.DatabaseName, &connection.DBUsername, &connection.SSLMode, &connection.IsActive, &connection.passwordEncrypted)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrConnectionNotFound
	}
	return connection, err
}

func (r connectionRepository) create(ctx context.Context, input ConnectionInput, encrypted string, createdBy int64) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO database_connections(name,host,port,database_name,db_username,password_encrypted,ssl_mode,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, input.Name, input.Host, input.Port, input.DatabaseName, input.DBUsername, encrypted, input.SSLMode, createdBy)
	return connectionDatabaseError(err)
}

func (r connectionRepository) update(ctx context.Context, id int64, input ConnectionInput, encrypted string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE database_connections SET name=$1,host=$2,port=$3,database_name=$4,db_username=$5,ssl_mode=$6,
		password_encrypted=CASE WHEN $7='' THEN password_encrypted ELSE $7 END,updated_at=now()
		WHERE id=$8`, input.Name, input.Host, input.Port, input.DatabaseName, input.DBUsername, input.SSLMode, encrypted, id)
	if err != nil {
		return connectionDatabaseError(err)
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrConnectionNotFound
	}
	return err
}

func (r connectionRepository) lockActive(ctx context.Context, id int64) (bool, error) {
	var active bool
	err := r.db.QueryRowContext(ctx, `SELECT is_active FROM database_connections WHERE id=$1 FOR UPDATE`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrConnectionNotFound
	}
	return active, err
}

func (r connectionRepository) setActive(ctx context.Context, id int64, active bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE database_connections SET is_active=$1,updated_at=now() WHERE id=$2`, active, id)
	return err
}

func (r connectionRepository) affectedUserIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ug.user_id FROM user_groups ug
		JOIN group_database_access gda ON gda.group_id=ug.group_id
		WHERE gda.database_id=$1 ORDER BY ug.user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		ids = append(ids, userID)
	}
	return ids, rows.Err()
}

func connectionDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "database_connections_name_key" {
		return ErrConnectionNameExists
	}
	return err
}
