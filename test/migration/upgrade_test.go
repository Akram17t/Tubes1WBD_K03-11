package migration_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	testdb "tubes-wbd/test/database"
)

// applySchema menjalankan file sql/0NN_*.sql dengan nomor from..to secara berurutan.
func applySchema(t *testing.T, db *sql.DB, from, to int) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "sql", "0*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("file schema tidak ditemukan: %v", err)
	}
	for _, file := range files {
		var number int
		name := filepath.Base(file)
		for _, r := range name[:3] {
			number = number*10 + int(r-'0')
		}
		if number < from || number > to {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(content)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestSchemaFreshInstallAndUpgradeFromM1(t *testing.T) {
	// Lock fixture bersama dipegang agar tidak bentrok dengan package lain; schema public di
	// database target test dipakai sebagai database platform sementara.
	testdb.Open(t)
	db := testdb.Target(t)

	t.Run("fresh install", func(t *testing.T) {
		applySchema(t, db, 1, 999)
		var tables int
		if err := db.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN
			('migrations','migration_executions','migration_requests','migration_jobs','migration_batch_attempts')`).Scan(&tables); err != nil || tables != 5 {
			t.Fatalf("tabel M2: %d %v", tables, err)
		}
		testdb.Exec(t, db, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	})

	// Data M1: A sukses (version 1), B gagal, C draft, D gagal lalu sukses (version 2), E masih running.
	applySchema(t, db, 1, 10)
	testdb.Exec(t, db, `
		INSERT INTO users (id,username,password_hash,display_name) VALUES (1,'lama','x','Lama');
		INSERT INTO database_connections (id,name,host,database_name,db_username,password_encrypted,ssl_mode,created_by)
		VALUES (1,'Lama','h','d','u','p','disable',1);
		INSERT INTO migrations (id,database_id,version,title,sql_content,status,created_by) VALUES
			(1,1,1,'A','CREATE TABLE a (id int)','success',1),
			(2,1,NULL,'B','CREATE TABLE b (','failed',1),
			(3,1,NULL,'C','CREATE TABLE c (id int)','draft',1),
			(4,1,2,'D','CREATE TABLE d (id int)','success',1),
			(5,1,NULL,'E','CREATE TABLE e (id int)','running',1);
		INSERT INTO migration_executions (id,migration_id,executed_by,sql_snapshot,status,started_at,finished_at,error_message) VALUES
			(1,1,1,'CREATE TABLE a (id int)','success','2026-10-01','2026-10-01',NULL),
			(2,2,1,'CREATE TABLE b (','failed','2026-10-02','2026-10-02','syntax error'),
			(3,4,1,'CREATE TABLE dd (id int)','failed','2026-10-03','2026-10-03','gagal'),
			(4,4,1,'CREATE TABLE d (id int)','success','2026-10-04','2026-10-04',NULL),
			(5,5,1,'CREATE TABLE e (id int)','running','2026-10-05',NULL,NULL);
		SELECT setval(pg_get_serial_sequence('migrations','id'), 5);
		SELECT setval(pg_get_serial_sequence('migration_executions','id'), 5);`)
	applySchema(t, db, 11, 999)

	// Semua definisi lama irreversible; yang pernah dieksekusi (termasuk gagal) langsung dikunci.
	var definitions string
	if err := db.QueryRow(`SELECT string_agg(title || ':' || revision || ':' || irreversible || ':' ||
		(locked_at IS NOT NULL) || ':' || (sql_down IS NULL), ' ' ORDER BY id) FROM migrations
		WHERE irreversible_reason IS NOT NULL AND mode = 'normal'`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if definitions != "A:1:true:true:true B:1:true:true:true C:1:true:false:true D:1:true:true:true E:1:true:true:true" {
		t.Fatalf("definisi hasil upgrade: %s", definitions)
	}

	// Attempt dinomori per migration; hanya yang sukses mendapat history_sequence dari version M1.
	var executions string
	if err := db.QueryRow(`SELECT string_agg(id || ':' || operation || ':' || attempt || ':' || database_id || ':' ||
		COALESCE(history_sequence::text, '-'), ' ' ORDER BY id) FROM migration_executions`).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if executions != "1:up:1:1:1 2:up:1:1:- 3:up:1:1:- 4:up:2:1:2 5:up:1:1:-" {
		t.Fatalf("execution hasil upgrade: %s", executions)
	}

	// SQL yang pernah dieksekusi immutable, draft tetap bisa diubah.
	if _, err := db.Exec(`UPDATE migrations SET sql_content = 'SELECT 1' WHERE id = 2`); err == nil ||
		!strings.Contains(err.Error(), "terkunci") {
		t.Fatalf("migration gagal M1 masih bisa diubah: %v", err)
	}
	testdb.Exec(t, db, `UPDATE migrations SET sql_content = 'CREATE TABLE c2 (id int)' WHERE id = 3`)

	// Jalur insert M1 tetap jalan: trigger melengkapi database, revisi, dan nomor attempt.
	var attempt int
	if err := db.QueryRow(`INSERT INTO migration_executions (migration_id, executed_by, sql_snapshot)
		VALUES (4, 1, 'x') RETURNING attempt`).Scan(&attempt); err != nil || attempt != 3 {
		t.Fatalf("insert gaya M1: %d %v", attempt, err)
	}
	// Execution running M1 (job_id NULL) tetap bisa dipulihkan oleh RecoverRunning.
	var running int
	if err := db.QueryRow(`SELECT count(*) FROM migration_executions WHERE status = 'running' AND job_id IS NULL`).Scan(&running); err != nil || running != 2 {
		t.Fatalf("execution running M1: %d %v", running, err)
	}
}
