package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
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

func TestExecuteRejectsLockedTargetAndPlatformAlias(t *testing.T) {
	f := testdb.New(t)
	m := draft(t, f, `INSERT INTO public.teams VALUES (99,'Tidak boleh dijalankan')`)
	alias, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer alias.Close()
	for _, target := range []*sql.DB{alias, f.Target} {
		if target == f.Target {
			conn, err := target.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(1)`); conn.Close() })
			if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(1)`); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := migration.ExecuteMigration(ctx, f.Platform, target, f.Editor, 1, m.ID)
		if !errors.Is(err, migration.ErrTargetUnavailable) || ctx.Err() != nil {
			cancel()
			t.Fatalf("target harus langsung ditolak tanpa menunggu deadline: %v", err)
		}
		cancel()
	}
	history, err := migration.GetExecutions(context.Background(), f.Platform, f.Editor, 1, m.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("target terkunci membuat percobaan baru: %+v %v", history, err)
	}
}

func TestRecoverRouteChecksPermissionsAndDoesNotRepeatSQL(t *testing.T) {
	f := testdb.New(t)
	ctx := context.Background()
	r := f.Router(t)
	migration.RegisterRoutes(r, f.Platform, f.Provider)
	editor := testdb.Login(t, r, "editor")
	viewer := testdb.Login(t, r, "viewer")
	m := draft(t, f, `INSERT INTO public.teams VALUES (99,'Hanya sekali')`)
	attempt, err := migration.ExecuteMigration(ctx, f.Platform, f.Target, f.Editor, 1, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	testdb.Exec(t, f.Platform, `UPDATE migrations SET status='running',version=NULL WHERE id=$1`, m.ID)
	testdb.Exec(t, f.Platform, `UPDATE migration_executions SET status='running',finished_at=NULL WHERE id=$1`, attempt.ID)
	path := fmt.Sprintf("/databases/1/migrations/%d", m.ID)
	page := testdb.Request(r, "GET", path, nil, editor)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Periksa hasil") {
		t.Fatalf("aksi recovery tidak tersedia: %d %s", page.Code, page.Body.String())
	}
	current, err := migration.GetMigration(ctx, f.Platform, f.Editor, 1, m.ID)
	if err != nil || current.Status != "running" {
		t.Fatal("GET mengubah status migration")
	}
	form := url.Values{"csrf_token": {testdb.Token(t, page.Body.String())}}
	if w := testdb.Request(r, "POST", path+"/recover", nil, editor); w.Code != 403 {
		t.Fatalf("recovery tanpa CSRF: %d", w.Code)
	}
	view := testdb.Request(r, "GET", path, nil, viewer)
	if strings.Contains(view.Body.String(), "Periksa hasil") {
		t.Fatal("viewer mendapat aksi recovery")
	}
	if w := testdb.Request(r, "POST", path+"/recover", url.Values{"csrf_token": {testdb.Token(t, view.Body.String())}}, viewer); w.Code != 403 {
		t.Fatalf("viewer dapat recovery: %d", w.Code)
	}
	if w := testdb.Request(r, "POST", fmt.Sprintf("/databases/2/migrations/%d/recover", m.ID), form, editor); w.Code != 404 {
		t.Fatalf("recovery lintas database: %d", w.Code)
	}
	conn, err := f.Target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(1)`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	w := testdb.Request(r, "POST", path+"/recover", form, editor)
	conn.ExecContext(ctx, `SELECT pg_advisory_unlock(1)`)
	conn.Close()
	current, err = migration.GetMigration(ctx, f.Platform, f.Editor, 1, m.ID)
	if w.Code != 303 || err != nil || current.Status != "running" {
		t.Fatal("recovery mengubah eksekusi yang masih terkunci")
	}
	for range 2 {
		if w := testdb.Request(r, "POST", path+"/recover", form, editor); w.Code != 303 || w.Header().Get("Location") != path {
			t.Fatalf("recovery: %d %s", w.Code, w.Body.String())
		}
	}
	current, err = migration.GetMigration(ctx, f.Platform, f.Editor, 1, m.ID)
	if err != nil || current.Status != "success" || current.Version.Int64 != 1 {
		t.Fatalf("status tidak pulih: %+v %v", current, err)
	}
	history, err := migration.GetExecutions(ctx, f.Platform, f.Editor, 1, m.ID)
	if err != nil || len(history) != 1 {
		t.Fatal("recovery mengeksekusi ulang SQL")
	}
	orphan := draft(t, f, `SELECT 1`)
	testdb.Exec(t, f.Platform, `UPDATE migrations SET status='running' WHERE id=$1`, orphan.ID)
	testdb.Exec(t, f.Platform, `INSERT INTO migration_executions(migration_id,executed_by,sql_snapshot) VALUES($1,1,'SELECT 1')`, orphan.ID)
	orphanPath := fmt.Sprintf("/databases/1/migrations/%d/recover", orphan.ID)
	r2 := f.Router(t)
	migration.RegisterRoutes(r2, f.Platform, nil)
	if w := testdb.Request(r2, "POST", orphanPath, form, editor); w.Code != 503 {
		t.Fatalf("recovery tanpa target: %d", w.Code)
	}
	if w := testdb.Request(r, "POST", orphanPath, form, editor); w.Code != 303 {
		t.Fatalf("recovery percobaan terputus: %d", w.Code)
	}
	current, err = migration.GetMigration(ctx, f.Platform, f.Editor, 1, orphan.ID)
	if err != nil || current.Status != "failed" || current.Version.Valid {
		t.Fatalf("hasil belum commit: %+v %v", current, err)
	}
}

func TestConnectionServiceViewerExecutionAndRecovery(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	f.Platform.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := f.Router(t)
	database.RegisterRoutes(r, f.Platform, service.Target)
	database.RegisterAdminRoutes(r, service)
	migration.RegisterRoutes(r, f.Platform, service.Target)
	editor := testdb.Login(t, r, "editor")
	viewer := testdb.Login(t, r, "viewer")
	for _, path := range []string{"/databases/1/schema?table=players", "/databases/1/tables/players/data"} {
		if w := testdb.Request(r, "GET", path, nil, viewer); w.Code != 200 {
			t.Fatalf("viewer dengan koneksi B: %d %s", w.Code, w.Body.String())
		}
	}
	page := testdb.Request(r, "GET", "/databases/1/migrations/new", nil, editor)
	form := url.Values{"csrf_token": {testdb.Token(t, page.Body.String())}, "title": {"Integrasi koneksi"}, "sql_content": {`CREATE TABLE public.integrated_table (id int PRIMARY KEY); INSERT INTO public.integrated_table VALUES (1)`}}
	w := testdb.Request(r, "POST", "/databases/1/migrations", form, editor)
	if w.Code != 303 {
		t.Fatalf("buat migration: %d %s", w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	w = testdb.Request(r, "POST", path+"/execute", form, editor)
	if w.Code != 303 {
		t.Fatalf("execute dengan koneksi B: %d %s", w.Code, w.Body.String())
	}
	if w := testdb.Request(r, "GET", "/databases/1/tables/integrated_table/data", nil, viewer); w.Code != 200 {
		t.Fatalf("viewer hasil migration: %d", w.Code)
	}
	var id int64
	if err := f.Platform.QueryRowContext(ctx, `SELECT id FROM migrations WHERE title='Integrasi koneksi'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	markPending := func() {
		t.Helper()
		testdb.Exec(t, f.Platform, `UPDATE migrations SET status='running',version=NULL WHERE id=$1`, id)
		testdb.Exec(t, f.Platform, `UPDATE migration_executions SET status='running',finished_at=NULL WHERE migration_id=$1`, id)
	}
	markPending()
	if err := migration.RecoverRunning(ctx, f.Platform, service.Target); err != nil {
		t.Fatalf("startup recovery dengan pool platform satu koneksi: %v", err)
	}
	markPending()
	if w := testdb.Request(r, "POST", path+"/recover", form, editor); w.Code != 303 {
		t.Fatalf("HTTP recovery dengan koneksi B: %d %s", w.Code, w.Body.String())
	}
	m, err := migration.GetMigration(ctx, f.Platform, f.Editor, 1, id)
	if err != nil || m.Status != "success" || m.Version.Int64 != 1 {
		t.Fatalf("hasil recovery integrasi: %+v %v", m, err)
	}
	var count int
	if err := f.Target.QueryRowContext(ctx, `SELECT COUNT(*) FROM public.integrated_table`).Scan(&count); err != nil || count != 1 {
		t.Fatal("recovery menjalankan ulang migration")
	}
	if err := service.SetActive(ctx, &auth.User{ID: 4, IsPlatformAdmin: true}, 1, false); err != nil {
		t.Fatal(err)
	}
	if w := testdb.Request(r, "GET", "/databases/1/schema", nil, viewer); w.Code != 303 {
		t.Fatalf("session masih bisa memakai target nonaktif: %d", w.Code)
	}
}
