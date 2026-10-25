package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func connect(t *testing.T, name string) *sql.DB {
	t.Helper()
	dsn := os.Getenv(name)
	if dsn == "" {
		t.Skip(name + " kosong, integration test dilewati")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.Contains(strings.ToLower(config.Database), "test") {
		t.Fatal(name + " harus menunjuk database khusus pengujian dengan nama yang mengandung 'test'")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Satu lock bersama mencegah fixture antar-package saling menghapus saat go test ./...
func Open(t *testing.T) *sql.DB {
	t.Helper()
	lockDB := connect(t, "TEST_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := lockDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(773102511)`); err != nil {
		t.Fatal(err)
	}
	db := connect(t, "TEST_DATABASE_URL")
	if _, err := db.Exec(`TRUNCATE users, groups, database_connections RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return db
}

func Target(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("TEST_TARGET_DATABASE_URL") == "" {
		t.Skip("TEST_TARGET_DATABASE_URL kosong, integration test dilewati")
	}
	platform, platformErr := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	target, targetErr := pgx.ParseConfig(os.Getenv("TEST_TARGET_DATABASE_URL"))
	if platformErr != nil || targetErr != nil || platform.Database == target.Database {
		t.Fatal("Database platform dan target pengujian harus berbeda")
	}
	db := connect(t, "TEST_TARGET_DATABASE_URL")
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	return db
}
