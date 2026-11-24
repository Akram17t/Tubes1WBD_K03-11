package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// batas panjang query di log biar satu baris log ga kebesaran
const maxLoggedQueryLen = 4000

// SQLEvent data satu eksekusi query yang mau dicatat. Rows cuma ditulis ke log kalau RowsKnown true.
type SQLEvent struct {
	Event     string
	Query     string
	Args      []any
	Started   time.Time
	Rows      int64
	RowsKnown bool
	Err       error
	Attrs     []any
}

// catat satu query ke log JSON: query, parameter tersamar, durasi, rows, error/SQLSTATE.
// Attrs buat field tambahan modul (database_id, job_id, execution_id, dll)
func LogSQL(ctx context.Context, e SQLEvent) {
	attrs := []any{
		"query", truncate(compactSQL(e.Query), maxLoggedQueryLen),
		"params", MaskArgs(e.Args),
		"duration_ms", float64(time.Since(e.Started).Microseconds()) / 1000,
	}
	if e.RowsKnown {
		attrs = append(attrs, "rows", e.Rows)
	}
	level := slog.LevelInfo
	status := "ok"
	if e.Err != nil {
		level, status = slog.LevelError, "error"
		attrs = append(attrs, "error", SafeError(e.Err))
		if code := SQLState(e.Err); code != "" {
			attrs = append(attrs, "sqlstate", code)
		}
	}
	attrs = append(attrs, "status", status)
	slog.Log(ctx, level, e.Event, append(attrs, e.Attrs...)...)
}

// ganti nilai parameter jadi tipe-nya aja (misal "$1:int64"), nilai asli ga pernah masuk log
func MaskArgs(args []any) []string {
	masked := make([]string, len(args))
	for i, arg := range args {
		masked[i] = fmt.Sprintf("$%d:%T", i+1, arg)
	}
	return masked
}

// ambil kode SQLSTATE dari error PostgreSQL, kosong kalau bukan error dari server
func SQLState(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

// pesan error yang aman di-log: buat error PostgreSQL cuma Message-nya,
// Detail sengaja dibuang karena sering berisi nilai row (misal duplicate key)
func SafeError(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Message
	}
	return err.Error()
}

// rapihin spasi/enter/tab dari indentasi kode Go jadi satu spasi biar query enak dibaca di log
func compactSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}

// potong string kepanjangan dan kasih tanda kalau kepotong
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}
