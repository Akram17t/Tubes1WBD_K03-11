package migration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"tubes-wbd/src/migration"
	testdb "tubes-wbd/test/database"
)

// seedSchema mengisi satu user, database, migration, dan request approved untuk menguji constraint tabel M2.
func seedSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	testdb.Exec(t, db, `
		INSERT INTO users (id,username,password_hash,display_name) VALUES (1,'editor','unused','Editor');
		INSERT INTO database_connections (id,name,host,database_name,db_username,password_encrypted,ssl_mode,created_by)
		VALUES (1,'Schema','localhost','schema','test','unused','disable',1);
		INSERT INTO migrations (id,database_id,title,sql_content,sql_down,created_by)
		VALUES (1,1,'A','CREATE TABLE a(id int)','DROP TABLE a',1);
		INSERT INTO migration_requests (id,database_id,migration_id,revision,operation,requested_by,definition_snapshot)
		VALUES (1,1,1,1,'up',1,'{}'),(2,1,1,1,'up',1,'{}');
	`)
}

func mustFail(t *testing.T, db *sql.DB, name, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("%s: constraint seharusnya menolak", name)
	}
}

func TestDefinitionConstraintsAndLock(t *testing.T) {
	db := testdb.Open(t)
	seedSchema(t, db)

	mustFail(t, db, "irreversible tanpa alasan", `UPDATE migrations SET irreversible=true WHERE id=1`)
	mustFail(t, db, "mode batch tanpa konfigurasi", `UPDATE migrations SET mode='batch' WHERE id=1`)
	mustFail(t, db, "mode tidak dikenal", `UPDATE migrations SET mode='stream' WHERE id=1`)
	mustFail(t, db, "sql down kosong", `UPDATE migrations SET sql_down='  ' WHERE id=1`)

	testdb.Exec(t, db, `UPDATE migrations SET irreversible=true, irreversible_reason='data dihapus' WHERE id=1`)
	testdb.Exec(t, db, `UPDATE migrations SET locked_at=now() WHERE id=1`)
	// setelah locked, SQL dan konfigurasi eksekusi tidak boleh berubah dan kunci tidak boleh dilepas
	mustFail(t, db, "ubah sql_up setelah locked", `UPDATE migrations SET sql_content='SELECT 2' WHERE id=1`)
	mustFail(t, db, "ubah sql_down setelah locked", `UPDATE migrations SET sql_down='SELECT 2' WHERE id=1`)
	mustFail(t, db, "ubah revision setelah locked", `UPDATE migrations SET revision=2 WHERE id=1`)
	mustFail(t, db, "lepas kunci", `UPDATE migrations SET locked_at=NULL WHERE id=1`)
	// metadata non-SQL tetap boleh berubah, misalnya status dan updated_at
	testdb.Exec(t, db, `UPDATE migrations SET status='running', updated_at=now() WHERE id=1`)
}

func TestJobConstraints(t *testing.T) {
	db := testdb.Open(t)
	seedSchema(t, db)
	insert := `INSERT INTO migration_jobs (request_id,database_id,migration_id,revision,operation,execution_actor_id,status,started_at,worker_token)
		VALUES ($1,1,1,1,'up',1,$2,now(),$3)`

	testdb.Exec(t, db, insert, 1, "queued", "t1")
	mustFail(t, db, "dua job untuk satu request", insert, 1, "queued", "t2")
	mustFail(t, db, "status tidak dikenal", insert, 2, "paused", "t2")
	testdb.Exec(t, db, `UPDATE migration_jobs SET status='running' WHERE request_id=1`)
	// satu database hanya boleh punya satu job running
	mustFail(t, db, "dua job running pada database sama", insert, 2, "running", "t2")
	mustFail(t, db, "running tanpa worker token", `INSERT INTO migration_jobs
		(request_id,database_id,migration_id,revision,operation,execution_actor_id,status,started_at)
		VALUES (2,1,1,1,'up',1,'running',now())`)
	mustFail(t, db, "job menjadi kompensasi dirinya sendiri", `UPDATE migration_jobs SET compensation_job_id=id`)
	// request kompensasi merujuk job yang ada
	mustFail(t, db, "source job tidak ada", `UPDATE migration_requests SET operation='compensate', source_job_id=999, definition_snapshot='{}' WHERE id=2`)
}

func TestExecutionHistoryConstraints(t *testing.T) {
	db := testdb.Open(t)
	seedSchema(t, db)
	testdb.Exec(t, db, `INSERT INTO migration_jobs (request_id,database_id,migration_id,revision,operation,execution_actor_id)
		VALUES (1,1,1,1,'up',1)`)
	// jalur insert M1 (tanpa database/revisi/attempt) tetap berjalan dan dilengkapi trigger
	testdb.Exec(t, db, `INSERT INTO migration_executions (migration_id,executed_by,sql_snapshot) VALUES (1,1,'SELECT 1')`)
	var databaseID, revision, attempt int
	if err := db.QueryRow(`SELECT database_id,revision,attempt FROM migration_executions`).Scan(&databaseID, &revision, &attempt); err != nil ||
		databaseID != 1 || revision != 1 || attempt != 1 {
		t.Fatalf("default insert M1: %d %d %d %v", databaseID, revision, attempt, err)
	}

	exec := `INSERT INTO migration_executions
		(migration_id,executed_by,sql_snapshot,job_id,request_id,database_id,revision,operation,attempt,status,finished_at,history_sequence,target_execution_id)
		VALUES (1,1,'SELECT 1',1,1,1,1,$1,$2,$3,now(),$4,$5)`
	testdb.Exec(t, db, `UPDATE migration_executions SET status='failed', finished_at=now()`)
	testdb.Exec(t, db, exec, "up", 1, "failed", nil, nil)
	mustFail(t, db, "attempt kembar pada satu job", exec, "up", 1, "failed", nil, nil)
	testdb.Exec(t, db, exec, "up", 2, "success", 1, nil)
	mustFail(t, db, "history_sequence kembar", exec, "up", 3, "success", 1, nil)
	mustFail(t, db, "history_sequence pada attempt gagal", exec, "up", 3, "failed", 2, nil)
	mustFail(t, db, "status tidak dikenal", exec, "up", 3, "done", nil, nil)
	mustFail(t, db, "rollback tanpa target", exec, "rollback", 3, "success", 2, nil)
	mustFail(t, db, "target pada operasi bukan rollback", exec, "up", 3, "failed", nil, 1)

	var successID int64
	if err := db.QueryRow(`SELECT id FROM migration_executions WHERE status='success'`).Scan(&successID); err != nil {
		t.Fatal(err)
	}
	testdb.Exec(t, db, exec, "rollback", 3, "success", 2, successID)
	// satu execution sukses hanya boleh dibatalkan oleh satu rollback sukses
	mustFail(t, db, "rollback sukses ganda", exec, "rollback", 4, "success", 3, successID)
	// rollback gagal boleh merujuk execution yang sama, dan posisi aktif tidak berubah
	testdb.Exec(t, db, exec, "rollback", 4, "failed", nil, successID)

	testdb.Exec(t, db, `INSERT INTO migration_batch_attempts (execution_id,job_id,batch_number,direction,first_id,last_id,row_count,sql_snapshot,status,finished_at)
		VALUES ($1,1,1,'up',1,1000,1000,'UPDATE t SET v=v+1 WHERE id=ANY($1)','success',now())`, successID)
	mustFail(t, db, "batch kembar pada satu execution", `INSERT INTO migration_batch_attempts
		(execution_id,job_id,batch_number,direction,sql_snapshot) VALUES ($1,1,1,'up','x')`, successID)
	mustFail(t, db, "rentang ID terbalik", `INSERT INTO migration_batch_attempts
		(execution_id,job_id,batch_number,direction,first_id,last_id,sql_snapshot) VALUES ($1,1,2,'up',10,1,'x')`, successID)
}

func TestTargetMetadataBootstrapIsIdempotent(t *testing.T) {
	target := testdb.Target(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// tabel M1 yang sudah berisi ledger harus tetap utuh setelah bootstrap
	testdb.Exec(t, target, `CREATE TABLE public._dbplatform_migrations (
		execution_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL,
		version BIGINT NOT NULL UNIQUE CHECK (version > 0), applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
		INSERT INTO public._dbplatform_migrations (execution_id, migration_id, version) VALUES (7, 3, 1)`)
	for i := 0; i < 3; i++ {
		if err := migration.EnsureTargetMetadata(ctx, target); err != nil {
			t.Fatalf("bootstrap ke-%d: %v", i+1, err)
		}
	}
	var operation string
	if err := target.QueryRow(`SELECT operation FROM public._dbplatform_migrations WHERE execution_id = 7`).Scan(&operation); err != nil || operation != "up" {
		t.Fatalf("baris ledger M1 berubah: %q %v", operation, err)
	}

	// bukti batch dan scope: satu batch asli tidak boleh dikompensasi dua kali
	testdb.Exec(t, target, `INSERT INTO public._dbplatform_batch_ledger
		(job_id,origin_job_id,execution_id,direction,batch_number,first_id,last_id,row_count)
		VALUES (5,5,10,'up',1,1,100,100), (6,5,11,'down',1,1,100,100)`)
	if _, err := target.Exec(`INSERT INTO public._dbplatform_batch_ledger
		(job_id,origin_job_id,execution_id,direction,batch_number,first_id,last_id,row_count)
		VALUES (7,5,12,'down',1,1,100,100)`); err == nil {
		t.Fatal("kompensasi ganda untuk batch yang sama harus ditolak")
	}
	if _, err := target.Exec(`INSERT INTO public._dbplatform_batch_ledger
		(job_id,origin_job_id,execution_id,direction,batch_number,first_id,last_id,row_count)
		VALUES (5,5,10,'up',1,1,100,100)`); err == nil {
		t.Fatal("batch up yang sama tidak boleh dicatat dua kali")
	}
	testdb.Exec(t, target, `INSERT INTO public._dbplatform_batch_scope (job_id,id) VALUES (5,1),(5,2)`)
	if _, err := target.Exec(`INSERT INTO public._dbplatform_batch_scope (job_id,id) VALUES (5,1)`); err == nil {
		t.Fatal("ID kembar pada scope satu job harus ditolak")
	}
}
