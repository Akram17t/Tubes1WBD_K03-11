package migration_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"
	testdb "tubes-wbd/test/database"
)

func draft(t *testing.T, f *testdb.Fixture, command string) *migration.Migration {
	t.Helper()
	m, err := migration.CreateMigration(context.Background(), f.Platform, f.Editor, 1, "Perubahan tabel", "", command)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExecutionRollbackRetryAndPermissions(t *testing.T) {
	f := testdb.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f.Platform.SetMaxOpenConns(1)
	f.Target.SetMaxOpenConns(1)
	m := draft(t, f, `CREATE TABLE public.new_table (id int); INSERT INTO public.new_table VALUES (1)`)
	for _, user := range []*auth.User{nil, f.Viewer, f.Outsider} {
		if _, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, user, 1, m.ID); !errors.Is(err, auth.ErrForbidden) {
			t.Fatalf("eksekusi tanpa izin: %v", err)
		}
	}
	if _, err := migration.GetMigration(ctx, f.Platform, f.Editor, 2, m.ID); !errors.Is(err, migration.ErrNotFound) {
		t.Fatalf("migration lintas database terbaca: %v", err)
	}
	execution, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, m.ID)
	if err != nil || execution.Status != "success" {
		t.Fatalf("eksekusi: %+v %v", execution, err)
	}
	if _, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, m.ID); !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("eksekusi ulang sukses: %v", err)
	}
	if _, err := migration.UpdateMigration(ctx, f.Platform, f.Editor, 1, m.ID, "Ubah", "", "SELECT 1"); !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("migration sukses dapat diedit: %v", err)
	}
	badSQL := `CREATE TABLE public.retry_table (id int); INSERT INTO missing_secret_table VALUES (1)`
	failed := draft(t, f, badSQL)
	attempt, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, failed.ID)
	if !errors.Is(err, migration.ErrExecutionFailed) || attempt.Status != "failed" || strings.Contains(attempt.ErrorMessage.String, "missing_secret_table") {
		t.Fatalf("kegagalan tidak aman/tercatat: %+v %v", attempt, err)
	}
	var exists bool
	if err := f.Target.QueryRowContext(ctx, `SELECT to_regclass('public.retry_table') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("DDL gagal tidak di-rollback: %v %v", exists, err)
	}
	if _, err := migration.UpdateMigration(ctx, f.Platform, f.Editor, 1, failed.ID, "Coba lagi", "", `CREATE TABLE public.retry_table (id int)`); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, failed.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := migration.GetMigration(ctx, f.Platform, f.Viewer, 1, failed.ID)
	if err != nil || updated.Version.Int64 != 2 {
		t.Fatalf("versi sukses: %+v %v", updated, err)
	}
	history, err := migration.GetExecutions(ctx, f.Platform, f.Viewer, 1, failed.ID)
	if err != nil || len(history) != 2 || history[1].SQLSnapshot != badSQL || history[1].Status != "failed" {
		t.Fatalf("snapshot riwayat berubah: %+v %v", history, err)
	}
	broken := draft(t, f, `CREATE TABLE public.must_rollback (id int); COMMIT`)
	if _, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, broken.ID); !errors.Is(err, migration.ErrExecutionFailed) {
		t.Fatalf("COMMIT dalam SQL diterima: %v", err)
	}
	if err := f.Target.QueryRowContext(ctx, `SELECT to_regclass('public.must_rollback') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("COMMIT memecah transaksi: %v %v", exists, err)
	}
}

func TestConcurrentExecutionOnlyRunsOnce(t *testing.T) {
	f := testdb.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	m := draft(t, f, `INSERT INTO public.teams VALUES (99,'Sekali saja')`)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, m.ID)
			results <- err
		}()
	}
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, migration.ErrConflict)) || (b == nil && errors.Is(a, migration.ErrConflict))) {
		t.Fatalf("dua eksekusi bersamaan: %v, %v", a, b)
	}
	history, err := migration.GetExecutions(ctx, f.Platform, f.Editor, 1, m.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("jumlah percobaan: %+v %v", history, err)
	}
}

func TestStartupRecoveryRespectsLocksAndCommitTracking(t *testing.T) {
	f := testdb.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	committed := draft(t, f, "SELECT 1")
	aborted := draft(t, f, "SELECT 2")
	var committedID int64
	for _, m := range []*migration.Migration{committed, aborted} {
		testdb.Exec(t, f.Platform, `UPDATE migrations SET status='running' WHERE id=$1`, m.ID)
		var id int64
		if err := f.Platform.QueryRowContext(ctx, `INSERT INTO migration_executions (migration_id,executed_by,sql_snapshot) VALUES ($1,1,$2) RETURNING id`, m.ID, m.SQLContent).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if m.ID == committed.ID {
			committedID = id
		}
	}
	testdb.Exec(t, f.Target, `CREATE TABLE public._dbplatform_migrations (execution_id bigint PRIMARY KEY,migration_id bigint,version bigint UNIQUE)`)
	testdb.Exec(t, f.Target, `INSERT INTO public._dbplatform_migrations VALUES ($1,$2,1)`, committedID, committed.ID)
	assertRunning := func() {
		t.Helper()
		m, err := migration.GetMigration(ctx, f.Platform, f.Viewer, 1, committed.ID)
		if err != nil || m.Status != "running" {
			t.Fatalf("status running berubah: %+v %v", m, err)
		}
	}
	assertRunning()
	if err := migration.RecoverRunning(ctx, f.Platform, nil); !errors.Is(err, database.ErrTargetUnavailable) {
		t.Fatalf("target belum tersedia: %v", err)
	}
	assertRunning()
	for _, pool := range []bool{false, true} {
		db := f.Platform
		if pool {
			db = f.Target
		}
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(1)`); err != nil {
			t.Fatal(err)
		}
		err = migration.RecoverRunning(ctx, f.Platform, f.Provider)
		conn.ExecContext(ctx, `SELECT pg_advisory_unlock(1)`)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		assertRunning()
	}
	f.Platform.SetMaxOpenConns(1)
	f.Target.SetMaxOpenConns(1)
	for range 2 {
		if err := migration.RecoverRunning(ctx, f.Platform, f.Provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id     int64
		status string
	}{{committed.ID, "success"}, {aborted.ID, "failed"}} {
		m, err := migration.GetMigration(ctx, f.Platform, f.Editor, 1, tc.id)
		if err != nil || m.Status != tc.status || (m.Version.Valid != (tc.status == "success")) {
			t.Fatalf("recovery: %+v %v", m, err)
		}
		history, err := migration.GetExecutions(ctx, f.Platform, f.Editor, 1, tc.id)
		if err != nil || len(history) != 1 || !history[0].FinishedAt.Valid {
			t.Fatalf("recovery menggandakan riwayat: %+v %v", history, err)
		}
	}
}

func TestExecuteRoutesCSRFPermissionsAndResult(t *testing.T) {
	f := testdb.New(t)
	r := f.Router(t)
	migration.RegisterRoutes(r, f.Platform, f.Provider)
	editor := testdb.Login(t, r, "editor")
	viewer := testdb.Login(t, r, "viewer")
	m := draft(t, f, `CREATE TABLE public.http_test (id int)`)
	path := fmt.Sprintf("/databases/1/migrations/%d", m.ID)
	page := testdb.Request(r, "GET", path, nil, editor)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Jalankan migration") || !strings.Contains(page.Body.String(), "<!doctype html>") {
		t.Fatalf("halaman detail: %d %s", page.Code, page.Body.String())
	}
	form := url.Values{"csrf_token": {testdb.Token(t, page.Body.String())}}
	if w := testdb.Request(r, "POST", path+"/execute", nil, editor); w.Code != 403 {
		t.Fatalf("CSRF kosong: %d", w.Code)
	}
	viewPage := testdb.Request(r, "GET", path, nil, viewer)
	if viewPage.Code != 200 || strings.Contains(viewPage.Body.String(), "Jalankan migration") {
		t.Fatal("viewer mendapat tombol execute")
	}
	viewForm := url.Values{"csrf_token": {testdb.Token(t, viewPage.Body.String())}}
	if w := testdb.Request(r, "POST", path+"/execute", viewForm, viewer); w.Code != 403 {
		t.Fatalf("viewer execute: %d", w.Code)
	}
	if w := testdb.Request(r, "POST", fmt.Sprintf("/databases/2/migrations/%d/execute", m.ID), form, editor); w.Code != 404 {
		t.Fatalf("execute lintas database: %d", w.Code)
	}
	w := testdb.Request(r, "POST", path+"/execute", form, editor)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "/executions/") {
		t.Fatalf("execute: %d %s", w.Code, w.Body.String())
	}
	result := testdb.Request(r, "GET", w.Header().Get("Location"), nil, viewer)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "success") {
		t.Fatalf("hasil execute: %d %s", result.Code, result.Body.String())
	}
	if w := testdb.Request(r, "POST", path+"/execute", form, editor); w.Code != 409 {
		t.Fatalf("execute ulang: %d", w.Code)
	}
	bad := draft(t, f, `SELECT * FROM missing_secret_table`)
	badPath := fmt.Sprintf("/databases/1/migrations/%d/execute", bad.ID)
	w = testdb.Request(r, "POST", badPath, form, editor)
	if w.Code != 303 {
		t.Fatalf("execute gagal: %d %s", w.Code, w.Body.String())
	}
	result = testdb.Request(r, "GET", w.Header().Get("Location"), nil, editor)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "SQLSTATE") {
		t.Fatalf("halaman kegagalan: %d %s", result.Code, result.Body.String())
	}
	r2 := f.Router(t)
	migration.RegisterRoutes(r2, f.Platform, nil)
	if w := testdb.Request(r2, "POST", badPath, form, editor); w.Code != 503 {
		t.Fatalf("koneksi belum disediakan: %d %s", w.Code, w.Body.String())
	}
}
