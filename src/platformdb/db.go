// Package platformdb manages the connection pool for the platform's own database.
package platformdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	maxOpenConnections = 10
	maxIdleConnections = 5
	maxConnectionAge   = 30 * time.Minute
	maxIdleTime        = 5 * time.Minute
	connectTimeout     = 5 * time.Second
)

// Connect opens and verifies a pool for the platform database. The caller owns
// the returned pool and must close it when the application shuts down.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("platform database DSN is empty")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open platform database: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConnections)
	db.SetMaxIdleConns(maxIdleConnections)
	db.SetConnMaxLifetime(maxConnectionAge)
	db.SetConnMaxIdleTime(maxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping platform database: %w", err)
	}

	return db, nil
}
