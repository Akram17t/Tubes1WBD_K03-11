package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/migration"
	testdb "tubes-wbd/test/database"
)

// Test di file ini memakai eksekutor asli: SQL benar-benar dijalankan pada database target.

func newExecutorFixture(t *testing.T) *queueFixture {
	t.Helper()
	f := newQueueFixture(t)
	f.config.Executor = migration.NewExecutor(f.Platform)
	return f
}

func (f *queueFixture) define(t *testing.T, input migration.DefinitionInput) int64 {
	t.Helper()
	definition, err := f.service.Create(context.Background(), f.Editor, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	return definition.ID
}

func normalDefinition(title, up, down string) migration.DefinitionInput {
	return migration.DefinitionInput{Title: title, SQLUp: up, SQLDown: &down, Mode: migration.ModeNormal}
}

// submitAndExecute membuat pengajuan, mengumpulkan approval kedua group, lalu menekan Eksekusi.
func (f *queueFixture) submitAndExecute(t *testing.T, input migration.RequestInput) *migration.Job {
	t.Helper()
	ctx := context.Background()
	if input.Revision == 0 {
		input.Revision = 1
	}
	request, err := f.workflow.Create(ctx, f.Editor, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.workflow.Submit(ctx, f.Editor, 1, request.ID); err != nil {
		t.Fatal(err)
	}
	for _, user := range []*auth.User{f.Editor, f.Viewer} {
		if _, err := f.workflow.Approve(ctx, user, 1, request.ID); err != nil {
			t.Fatal(err)
		}
	}
	job, err := f.workflow.Execute(ctx, f.Editor, 1, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *queueFixture) run(t *testing.T, input migration.RequestInput) *migration.Job {
	t.Helper()
	job := f.submitAndExecute(t, input)
	f.drain(t, f.worker(t), job.ID)
	return job
}

type executionRow struct {
	id                  int64
	status, operation   string
	sequence, target    sql.NullInt64
	sqlState            sql.NullString
	committed, reverted int64
}

func (f *queueFixture) executions(t *testing.T, jobID int64) []executionRow {
	t.Helper()
	rows, err := f.Platform.Query(`SELECT id, status, operation, history_sequence, target_execution_id, sqlstate,
		committed_batches, reverted_batches FROM migration_executions WHERE job_id = $1 ORDER BY attempt`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []executionRow
	for rows.Next() {
		var row executionRow
		if err := rows.Scan(&row.id, &row.status, &row.operation, &row.sequence, &row.target, &row.sqlState,
			&row.committed, &row.reverted); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	return result
}

func (f *queueFixture) tableExists(t *testing.T, name string) bool {
	t.Helper()
	var exists bool
	if err := f.Target.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestExecutorNormalUpRollbackForward(t *testing.T) {
	f := newExecutorFixture(t)
	a := f.define(t, normalDefinition("A", "CREATE TABLE public.exec_a (id int)", "DROP TABLE public.exec_a"))
	b := f.define(t, normalDefinition("B", "CREATE TABLE public.exec_b (id int); INSERT INTO public.exec_b VALUES (1)",
		"DROP TABLE public.exec_b"))

	upA := f.run(t, migration.RequestInput{MigrationID: a, Operation: "up"})
	upB := f.run(t, migration.RequestInput{MigrationID: b, Operation: "up"})
	f.expectJob(t, upA.ID, migration.JobSuccess, "", 1)
	f.expectJob(t, upB.ID, migration.JobSuccess, "", 1)
	if !f.tableExists(t, "exec_a") || !f.tableExists(t, "exec_b") {
		t.Fatal("SQL up tidak diterapkan")
	}
	appliedB := f.executions(t, upB.ID)[0]

	rollback := f.run(t, migration.RequestInput{MigrationID: b, Operation: "rollback", TargetExecutionID: &appliedB.id})
	f.expectJob(t, rollback.ID, migration.JobSuccess, "", 1)
	if f.tableExists(t, "exec_b") {
		t.Fatal("rollback tidak menjalankan SQL down")
	}
	undo := f.executions(t, rollback.ID)[0]
	if undo.operation != "rollback" || undo.target.Int64 != appliedB.id {
		t.Fatalf("execution rollback: %+v", undo)
	}

	forward := f.run(t, migration.RequestInput{MigrationID: b, Operation: "forward", TargetExecutionID: &appliedB.id})
	f.expectJob(t, forward.ID, migration.JobSuccess, "", 1)
	if !f.tableExists(t, "exec_b") {
		t.Fatal("forward tidak menjalankan SQL up asli")
	}

	// history_sequence naik per event sukses dan ledger target memuat bukti setiap operasi.
	var sequences []int64
	for _, job := range []int64{upA.ID, upB.ID, rollback.ID, forward.ID} {
		row := f.executions(t, job)[0]
		if row.status != "success" || !row.sequence.Valid {
			t.Fatalf("job %d: %+v", job, row)
		}
		sequences = append(sequences, row.sequence.Int64)
	}
	if fmt.Sprint(sequences) != "[1 2 3 4]" {
		t.Fatalf("history_sequence %v", sequences)
	}
	var markers int
	if err := f.Target.QueryRow(`SELECT count(*) FROM public._dbplatform_migrations
		WHERE operation = 'rollback' AND target_execution_id = $1`, appliedB.id).Scan(&markers); err != nil || markers != 1 {
		t.Fatalf("marker rollback: %d %v", markers, err)
	}

	// Posisi aktif sekarang A lalu forward B, sehingga rollback B lama (execution up) sudah bukan puncak.
	_, err := f.workflow.Create(context.Background(), f.Editor, 1,
		migration.RequestInput{MigrationID: b, Revision: 1, Operation: "rollback", TargetExecutionID: &appliedB.id})
	if !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("rollback execution yang sudah dibatalkan: %v", err)
	}
}

func TestExecutorNormalFailureRollsBackWholeTransaction(t *testing.T) {
	f := newExecutorFixture(t)
	id := f.define(t, normalDefinition("Gagal",
		"CREATE TABLE public.exec_partial (id int); INSERT INTO public.exec_missing VALUES (1)",
		"DROP TABLE public.exec_partial"))
	job := f.run(t, migration.RequestInput{MigrationID: id, Operation: "up"})

	f.expectJob(t, job.ID, migration.JobFailed, migration.JobErrorSQL, 1)
	rows := f.executions(t, job.ID)
	if len(rows) != 1 || rows[0].status != "failed" || rows[0].sqlState.String != "42P01" || rows[0].sequence.Valid {
		t.Fatalf("execution gagal: %+v", rows)
	}
	if f.tableExists(t, "exec_partial") {
		t.Fatal("statement pertama tetap committed walaupun statement kedua gagal")
	}
	var markers int
	if err := f.Target.QueryRow(`SELECT count(*) FROM public._dbplatform_migrations`).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("marker ledger: %d %v", markers, err)
	}
	// Kesalahan SQL permanen tidak di-retry dan definisi tetap terkunci.
	url := fmt.Sprintf("/api/databases/1/jobs/%d", job.ID)
	detail := object(t, f.call(t, f.editor, "GET", url, "", 200, "").Data)
	if detail["status"] != "failed" || fmt.Sprint(detail["error"]) != "map[code:sql_error message:relation \"public.exec_missing\" does not exist sqlstate:42P01]" {
		t.Fatalf("detail job gagal: %v", detail)
	}
}

// batchFixture menyiapkan 25 row bernilai 0, batch berukuran 10, dan trigger untuk memaksa batch gagal.
func newBatchFixture(t *testing.T) (*queueFixture, int64) {
	t.Helper()
	f := newExecutorFixture(t)
	testdb.Exec(t, f.Target, `
		CREATE TABLE public.batch_demo (id BIGINT PRIMARY KEY, value BIGINT NOT NULL);
		INSERT INTO public.batch_demo SELECT n, 0 FROM generate_series(1, 25) n;
		CREATE TABLE public.batch_fail (min_id BIGINT, max_id BIGINT);
		CREATE FUNCTION public.batch_guard() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF EXISTS (SELECT 1 FROM public.batch_fail WHERE NEW.id BETWEEN min_id AND max_id) THEN
				RAISE EXCEPTION 'batch dipaksa gagal' USING ERRCODE = 'check_violation';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER batch_guard BEFORE UPDATE ON public.batch_demo
			FOR EACH ROW EXECUTE FUNCTION public.batch_guard();`)
	size := 10
	down := "UPDATE public.batch_demo SET value = value - 1 WHERE id = ANY($1::bigint[])"
	id := f.define(t, migration.DefinitionInput{Title: "Batch", Mode: migration.ModeBatch,
		SQLUp:       "UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1::bigint[])",
		SQLDown:     &down,
		BatchConfig: &migration.BatchConfig{Schema: "public", Table: "batch_demo", KeyColumn: "id", BatchSize: &size}})
	return f, id
}

// values mengembalikan nilai per rentang ID, misalnya "1-20:1 21-25:0".
func (f *queueFixture) values(t *testing.T) string {
	t.Helper()
	var summary string
	if err := f.Target.QueryRow(`SELECT string_agg(range, ' ' ORDER BY first) FROM (
		SELECT min(id) AS first, min(id) || '-' || max(id) || ':' || value AS range FROM (
			SELECT id, value, id - row_number() OVER (PARTITION BY value ORDER BY id) AS grp
			FROM public.batch_demo) g GROUP BY value, grp) r`).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	return summary
}

func (f *queueFixture) progress(t *testing.T, jobID int64) string {
	t.Helper()
	var total sql.NullInt64
	var rows, batches, revertedRows, revertedBatches int64
	if err := f.Platform.QueryRow(`SELECT total_rows, committed_rows, committed_batches, reverted_rows, reverted_batches
		FROM migration_jobs WHERE id = $1`, jobID).Scan(&total, &rows, &batches, &revertedRows, &revertedBatches); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("total=%d committed=%d/%d reverted=%d/%d", total.Int64, rows, batches, revertedRows, revertedBatches)
}

func (f *queueFixture) resume(t *testing.T, jobID int64) {
	t.Helper()
	if _, err := f.jobs.Resume(context.Background(), f.Editor, 1, jobID); err != nil {
		t.Fatal(err)
	}
	f.drain(t, f.worker(t), jobID)
}

func TestExecutorBatchFailureKeepsCommittedBatchesAndResumes(t *testing.T) {
	f, id := newBatchFixture(t)
	testdb.Exec(t, f.Target, `INSERT INTO public.batch_fail VALUES (21, 25)`)
	job := f.run(t, migration.RequestInput{MigrationID: id, Operation: "up"})

	f.expectJob(t, job.ID, migration.JobPartial, migration.JobErrorSQL, 1)
	if got := f.values(t); got != "1-20:1 21-25:0" {
		t.Fatalf("batch committed tidak bertahan: %s", got)
	}
	if got := f.progress(t, job.ID); got != "total=25 committed=20/2 reverted=0/0" {
		t.Fatalf("progress partial: %s", got)
	}
	var attempts string
	if err := f.Platform.QueryRow(`SELECT string_agg(batch_number || ':' || status, ' ' ORDER BY id)
		FROM migration_batch_attempts WHERE job_id = $1`, job.ID).Scan(&attempts); err != nil || attempts != "1:success 2:success 3:failed" {
		t.Fatalf("batch attempts: %s %v", attempts, err)
	}

	// Partial menahan migration lain pada database yang sama.
	other := f.define(t, normalDefinition("Lain", "CREATE TABLE public.exec_other (id int)", "DROP TABLE public.exec_other"))
	_, err := f.workflow.Create(context.Background(), f.Editor, 1, migration.RequestInput{MigrationID: other, Revision: 1, Operation: "up"})
	if definitionErr, ok := errors.AsType[*migration.DefinitionError](err); !ok || definitionErr.Code != "database_blocked" {
		t.Fatalf("partial tidak memblokir: %v", err)
	}

	// Row baru setelah scope dibekukan tidak ikut; resume melanjutkan batch 3 tanpa mengulang batch 1-2.
	testdb.Exec(t, f.Target, `DELETE FROM public.batch_fail; INSERT INTO public.batch_demo VALUES (26, 0)`)
	f.resume(t, job.ID)
	f.expectJob(t, job.ID, migration.JobSuccess, "", 2)
	if got := f.values(t); got != "1-25:1 26-26:0" {
		t.Fatalf("resume mengulang atau memperluas scope: %s", got)
	}
	if got := f.progress(t, job.ID); got != "total=25 committed=25/3 reverted=0/0" {
		t.Fatalf("progress akhir: %s", got)
	}
	rows := f.executions(t, job.ID)
	if len(rows) != 2 || rows[0].status != "partial" || rows[1].status != "success" || !rows[1].sequence.Valid {
		t.Fatalf("history attempt: %+v", rows)
	}
}

func TestExecutorCompensationReverseOrderAndPartiallyReverted(t *testing.T) {
	f, id := newBatchFixture(t)
	testdb.Exec(t, f.Target, `INSERT INTO public.batch_fail VALUES (21, 25)`)
	source := f.run(t, migration.RequestInput{MigrationID: id, Operation: "up"})
	f.expectJob(t, source.ID, migration.JobPartial, migration.JobErrorSQL, 1)

	// Kompensasi gagal di batch 1 setelah batch 2 dibatalkan: partially_reverted dan tetap memblokir.
	testdb.Exec(t, f.Target, `DELETE FROM public.batch_fail; INSERT INTO public.batch_fail VALUES (1, 10)`)
	compensation := f.run(t, migration.RequestInput{MigrationID: id, Operation: "compensate", SourceJobID: &source.ID})
	f.expectJob(t, compensation.ID, migration.JobPartiallyReverted, migration.JobErrorSQL, 1)
	if got := f.values(t); got != "1-10:1 11-25:0" {
		t.Fatalf("kompensasi sebagian: %s", got)
	}
	if got := f.progress(t, compensation.ID); got != "total=20 committed=0/0 reverted=10/1" {
		t.Fatalf("progress kompensasi: %s", got)
	}
	f.expectJob(t, source.ID, migration.JobPartial, migration.JobErrorSQL, 1)

	testdb.Exec(t, f.Target, `DELETE FROM public.batch_fail`)
	f.resume(t, compensation.ID)
	f.expectJob(t, compensation.ID, migration.JobSuccess, "", 2)
	f.expectJob(t, source.ID, migration.JobCompensated, migration.JobErrorSQL, 1)
	if got := f.values(t); got != "1-25:0" {
		t.Fatalf("kompensasi tidak tuntas: %s", got)
	}
	var order string
	if err := f.Platform.QueryRow(`SELECT string_agg(batch_number || ':' || status, ' ' ORDER BY id)
		FROM migration_batch_attempts WHERE job_id = $1`, compensation.ID).Scan(&order); err != nil || order != "2:success 1:failed 1:success" {
		t.Fatalf("urutan kompensasi: %s %v", order, err)
	}
	// Setelah kompensasi tuntas database tidak lagi tertahan.
	other := f.define(t, normalDefinition("Lain", "CREATE TABLE public.exec_other (id int)", "DROP TABLE public.exec_other"))
	if job := f.run(t, migration.RequestInput{MigrationID: other, Operation: "up"}); f.row(t, job.ID).status != migration.JobSuccess {
		t.Fatal("database masih tertahan setelah kompensasi")
	}
}

func TestExecutorBatchRollbackAndForwardUseOriginalScope(t *testing.T) {
	f, id := newBatchFixture(t)
	up := f.run(t, migration.RequestInput{MigrationID: id, Operation: "up"})
	f.expectJob(t, up.ID, migration.JobSuccess, "", 1)
	applied := f.executions(t, up.ID)[0]

	testdb.Exec(t, f.Target, `INSERT INTO public.batch_demo VALUES (26, 7)`)
	rollback := f.run(t, migration.RequestInput{MigrationID: id, Operation: "rollback", TargetExecutionID: &applied.id})
	f.expectJob(t, rollback.ID, migration.JobSuccess, "", 1)
	if got := f.values(t); got != "1-25:0 26-26:7" {
		t.Fatalf("rollback batch: %s", got)
	}
	if got := f.progress(t, rollback.ID); got != "total=25 committed=0/0 reverted=25/3" {
		t.Fatalf("progress rollback: %s", got)
	}

	forward := f.run(t, migration.RequestInput{MigrationID: id, Operation: "forward", TargetExecutionID: &applied.id})
	f.expectJob(t, forward.ID, migration.JobSuccess, "", 1)
	if got := f.values(t); got != "1-25:1 26-26:7" {
		t.Fatalf("forward memakai scope baru: %s", got)
	}
	// Rollback atas forward menunjuk execution forward dan scope yang sama.
	forwarded := f.executions(t, forward.ID)[0]
	again := f.run(t, migration.RequestInput{MigrationID: id, Operation: "rollback", TargetExecutionID: &forwarded.id})
	f.expectJob(t, again.ID, migration.JobSuccess, "", 1)
	if got := f.values(t); got != "1-25:0 26-26:7" {
		t.Fatalf("rollback forward: %s", got)
	}
}

// lossyExecutor menjalankan eksekutor asli lalu membuang Record, seolah platform mati setelah target commit.
type lossyExecutor struct {
	*migration.Executor
	lose bool
}

func (l *lossyExecutor) Execute(ctx context.Context, task *migration.JobTask) migration.JobResult {
	result := l.Executor.Execute(ctx, task)
	if !l.lose {
		return result
	}
	return migration.JobResult{Status: migration.JobOutcomeUnknown, ErrorCode: migration.JobErrorOutcomeUnknown,
		ErrorMessage: "platform gagal mencatat"}
}

func (f *queueFixture) recoverUntil(t *testing.T, worker *migration.Worker, jobID int64, status string) {
	t.Helper()
	for range 100 {
		if _, err := worker.RecoverOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.row(t, jobID).status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %d tidak menjadi %s: %+v", jobID, status, f.row(t, jobID))
}

func TestExecutorReconcilesTargetCommitWithoutPlatformRecord(t *testing.T) {
	f := newExecutorFixture(t)
	lossy := &lossyExecutor{Executor: migration.NewExecutor(f.Platform), lose: true}
	f.config.Executor = lossy
	id := f.define(t, normalDefinition("Hilang", "CREATE TABLE public.exec_lost (id int)", "DROP TABLE public.exec_lost"))
	job := f.submitAndExecute(t, migration.RequestInput{MigrationID: id, Operation: "up"})
	worker := f.worker(t)
	runOnce(t, worker)

	row := f.expectJob(t, job.ID, migration.JobOutcomeUnknown, migration.JobErrorOutcomeUnknown, 1)
	if row.openAttempt != 1 || !f.tableExists(t, "exec_lost") {
		t.Fatalf("simulasi tidak sesuai: %+v", row)
	}
	// outcome_unknown menahan database: job lain tidak diklaim sampai rekonsiliasi.
	other := f.define(t, normalDefinition("Lain", "CREATE TABLE public.exec_other (id int)", "DROP TABLE public.exec_other"))
	if _, err := f.workflow.Create(context.Background(), f.Editor, 1,
		migration.RequestInput{MigrationID: other, Revision: 1, Operation: "up"}); !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("outcome_unknown tidak memblokir: %v", err)
	}

	// Rekonsiliasi membaca ledger dan tidak menjalankan CREATE TABLE lagi (yang akan gagal).
	lossy.lose = false
	f.recoverUntil(t, worker, job.ID, migration.JobSuccess)
	f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
	rows := f.executions(t, job.ID)
	if len(rows) != 1 || rows[0].status != "success" || rows[0].sequence.Int64 != 1 {
		t.Fatalf("hasil rekonsiliasi: %+v", rows)
	}
	next := f.run(t, migration.RequestInput{MigrationID: other, Operation: "up"})
	f.expectJob(t, next.ID, migration.JobSuccess, "", 1)
	if f.executions(t, next.ID)[0].sequence.Int64 != 2 {
		t.Fatal("history_sequence setelah rekonsiliasi tidak berlanjut")
	}
}

func TestExecutorRecoveryResumesBatchFromLedgerCheckpoint(t *testing.T) {
	f, id := newBatchFixture(t)
	lossy := &lossyExecutor{Executor: migration.NewExecutor(f.Platform), lose: true}
	f.config.Executor = lossy
	testdb.Exec(t, f.Target, `INSERT INTO public.batch_fail VALUES (21, 25)`)
	job := f.submitAndExecute(t, migration.RequestInput{MigrationID: id, Operation: "up"})
	worker := f.worker(t)
	runOnce(t, worker)
	f.expectJob(t, job.ID, migration.JobOutcomeUnknown, migration.JobErrorOutcomeUnknown, 1)

	// Hasil attempt tidak tercatat; ledger target membuktikan dua batch committed dan batch 3 tidak.
	testdb.Exec(t, f.Target, `DELETE FROM public.batch_fail`)
	lossy.lose = false
	f.recoverUntil(t, worker, job.ID, migration.JobQueued)
	if got := f.progress(t, job.ID); got != "total=25 committed=20/2 reverted=0/0" {
		t.Fatalf("progress hasil rekonsiliasi: %s", got)
	}
	f.drain(t, worker, job.ID)
	f.expectJob(t, job.ID, migration.JobSuccess, "", 2)
	if got := f.values(t); got != "1-25:1" {
		t.Fatalf("batch committed diulang setelah recovery: %s", got)
	}
	rows := f.executions(t, job.ID)
	if len(rows) != 2 || rows[0].status != "partial" || rows[0].committed != 2 || rows[1].status != "success" {
		t.Fatalf("history recovery: %+v", rows)
	}
}
