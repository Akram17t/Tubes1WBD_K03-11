package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TargetProvider mengambil pool target milik pengelola koneksi berdasarkan dbID
type TargetProvider func(context.Context, int64) (*sql.DB, error)

var ErrTargetUnavailable = errors.New("koneksi database target belum tersedia atau tidak dapat dihubungi")

// TargetConnection memeriksa hasil provider tanpa membocorkan detail koneksi
func TargetConnection(ctx context.Context, platform *sql.DB, provider TargetProvider, dbID int64) (*sql.DB, error) {
	if provider == nil {
		return nil, ErrTargetUnavailable
	}
	db, err := provider(ctx, dbID)
	if err != nil || db == nil || db == platform {
		return nil, ErrTargetUnavailable
	}
	return db, nil
}

func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
