package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/migration"
	testdb "tubes-wbd/test/database"
)

// fakeExecutor mengikuti kontrak JobExecutor: row running di-commit sebelum "SQL", lalu
// difinalisasi lewat Record. Perilaku SQL diatur tiap test.
type fakeExecutor struct {
	db        *sql.DB
	mu        sync.Mutex
	tasks     []migration.JobTask
	reconcile []migration.JobTask
	run       func(context.Context, *migration.JobTask) migration.JobResult
	verify    func(context.Context, *migration.JobTask) migration.JobResult
}

func (f *fakeExecutor) record(result migration.JobResult, jobID int64, attempt int) migration.JobResult {
	if result.Status == migration.JobOutcomeUnknown {
		return result
	}
	status := result.Status
	result.Record = func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE migration_executions SET status = $3, finished_at = now()
			WHERE job_id = $1 AND attempt = $2 AND status = 'running'`, jobID, attempt, status)
		return err
	}
	return result
}

func (f *fakeExecutor) Execute(ctx context.Context, task *migration.JobTask) migration.JobResult {
	if _, err := f.db.ExecContext(ctx, `INSERT INTO migration_executions
		(migration_id, executed_by, sql_snapshot, status, job_id, request_id, database_id, revision, operation, attempt)
		VALUES ($1,$2,$3,'running',$4,$5,$6,$7,$8,$9)`, task.MigrationID, task.ActorID, task.Snapshot.SQLUp,
		task.JobID, task.RequestID, task.DatabaseID, task.Revision, task.Operation, task.Attempt); err != nil {
		return migration.JobResult{Status: migration.JobOutcomeUnknown, ErrorCode: "test", ErrorMessage: err.Error()}
	}
	f.mu.Lock()
	f.tasks = append(f.tasks, *task)
	run := f.run
	f.mu.Unlock()
	result := migration.JobResult{Status: migration.JobSuccess}
	if run != nil {
		result = run(ctx, task)
	}
	return f.record(result, task.JobID, task.Attempt)
}

func (f *fakeExecutor) Reconcile(ctx context.Context, task *migration.JobTask) migration.JobResult {
	f.mu.Lock()
	f.reconcile = append(f.reconcile, *task)
	f.mu.Unlock()
	return f.record(f.verify(ctx, task), task.JobID, task.Attempt)
}

func (f *fakeExecutor) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tasks)
}

type queueFixture struct {
	*definitionFixture
	jobs     migration.JobService
	executor *fakeExecutor
	config   migration.WorkerConfig
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	f := &queueFixture{definitionFixture: newDefinitionFixture(t)}
	testdb.Exec(t, f.Platform, `UPDATE groups SET leader_user_id = id`)
	f.jobs = migration.JobService{DB: f.Platform, Definitions: f.service}
	f.workflow.Enqueue = f.jobs.Enqueue
	// Router dibuat ulang agar route workflow memakai Enqueue.
	f.router = f.Router(t)
	if err := migration.RegisterDefinitionRoutes(f.router, f.service, auth.CSRF()); err != nil {
		t.Fatal(err)
	}
	if err := migration.RegisterWorkflowRoutes(f.router, f.workflow, auth.CSRF()); err != nil {
		t.Fatal(err)
	}
	if err := migration.RegisterJobRoutes(f.router, f.jobs, auth.CSRF()); err != nil {
		t.Fatal(err)
	}
	f.editor, f.viewer = f.login(t, "editor"), f.login(t, "viewer")
	f.executor = &fakeExecutor{db: f.Platform}
	// Kedua koneksi test menunjuk server yang sama; advisory lock tetap berbeda per database_id.
	targets := func(context.Context, int64) (*sql.DB, error) { return f.Target, nil }
	f.config = migration.WorkerConfig{
		DB: f.Platform, Targets: targets, Definitions: f.service, Executor: f.executor,
		Concurrency: 2, PollInterval: 20 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond, StaleAfter: time.Second,
		RetryDelays: []time.Duration{time.Millisecond, time.Millisecond}, GracePeriod: 5 * time.Second,
	}
	return f
}

func (f *queueFixture) worker(t *testing.T) *migration.Worker {
	t.Helper()
	worker, err := migration.NewWorker(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// approved membuat definisi baru dan pengajuan up yang sudah disetujui semua leader.
func (f *queueFixture) approved(t *testing.T, databaseID int64) *migration.MigrationRequest {
	t.Helper()
	ctx := context.Background()
	down := "DROP TABLE public.queue_demo"
	definition, err := f.service.Create(ctx, f.Editor, databaseID, migration.DefinitionInput{
		Title: "Queue", SQLUp: "CREATE TABLE public.queue_demo (id int)", SQLDown: &down, Mode: "normal",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.workflow.Create(ctx, f.Editor, databaseID,
		migration.RequestInput{MigrationID: definition.ID, Revision: 1, Operation: "up"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.workflow.Submit(ctx, f.Editor, databaseID, request.ID); err != nil {
		t.Fatal(err)
	}
	for _, user := range []*auth.User{f.Editor, f.Viewer} {
		if _, err := f.workflow.Approve(ctx, user, databaseID, request.ID); err != nil &&
			!(databaseID == 2 && errors.Is(err, auth.ErrForbidden)) {
			t.Fatal(err)
		}
	}
	return request
}

func (f *queueFixture) enqueue(t *testing.T, databaseID int64) *migration.Job {
	t.Helper()
	request := f.approved(t, databaseID)
	job, err := f.workflow.Execute(context.Background(), f.Editor, databaseID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

type jobRow struct {
	status, errorCode       string
	attempts, auto          int
	executions, openAttempt int
}

func (f *queueFixture) row(t *testing.T, jobID int64) jobRow {
	t.Helper()
	var row jobRow
	if err := f.Platform.QueryRow(`SELECT status, COALESCE(error_code,''), attempt_count, auto_attempt_count,
		(SELECT count(*) FROM migration_executions WHERE job_id = j.id),
		(SELECT count(*) FROM migration_executions WHERE job_id = j.id AND status = 'running')
		FROM migration_jobs j WHERE id = $1`, jobID).Scan(&row.status, &row.errorCode, &row.attempts, &row.auto,
		&row.executions, &row.openAttempt); err != nil {
		t.Fatal(err)
	}
	return row
}

func (f *queueFixture) expectJob(t *testing.T, jobID int64, status, code string, attempts int) jobRow {
	t.Helper()
	row := f.row(t, jobID)
	if row.status != status || row.errorCode != code || row.attempts != attempts {
		t.Fatalf("job %d: %+v, harusnya status=%s code=%q attempt=%d", jobID, row, status, code, attempts)
	}
	return row
}

func runOnce(t *testing.T, worker *migration.Worker) bool {
	t.Helper()
	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ran
}

// drain menjalankan job sampai tidak ada lagi yang antre, termasuk retry yang sudah jatuh tempo.
func (f *queueFixture) drain(t *testing.T, worker *migration.Worker, jobID int64) {
	t.Helper()
	for range 20 {
		if _, err := worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.row(t, jobID).status != migration.JobQueued {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job masih antre")
}

func waitFor(t *testing.T, signal <-chan int64) int64 {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("eksekutor tidak dipanggil")
		return 0
	}
}

func TestExecuteEnqueuesOnceAndLocksDefinition(t *testing.T) {
	f := newQueueFixture(t)
	request := f.approved(t, 1)
	path := fmt.Sprintf("/api/databases/1/requests/%d/execute", request.ID)

	first := object(t, f.call(t, f.editor, "POST", path, "", 202, "").Data)
	second := object(t, f.call(t, f.editor, "POST", path, "", 202, "").Data)
	if first["id"] != second["id"] || first["status"] != "queued" || first["attempt_count"] != float64(0) {
		t.Fatalf("klik ganda: %v %v", first, second)
	}
	if f.executor.calls() != 0 {
		t.Fatal("handler HTTP menjalankan eksekutor")
	}
	var jobs int
	var locked bool
	var carrier string
	if err := f.Platform.QueryRow(`SELECT (SELECT count(*) FROM migration_jobs),
		(SELECT locked_at IS NOT NULL FROM migrations WHERE id = $1),
		(SELECT trace_carrier::text FROM migration_jobs LIMIT 1)`, request.MigrationID).Scan(&jobs, &locked, &carrier); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || !locked || carrier != "{}" {
		t.Fatalf("jobs=%d locked=%v carrier=%s", jobs, locked, carrier)
	}
	f.call(t, f.editor, "PATCH", fmt.Sprintf("%s/%d", base, request.MigrationID), `{"revision":1,"title":"x"}`, 409, "immutable_migration")
	if _, err := migration.ExecuteMigration(context.Background(), f.Platform, f.Target, f.Editor, 1, request.MigrationID); !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("jalur M1 menyusul queue: %v", err)
	}

	url := "/api/databases/1/jobs/" + first["id"].(string)
	detail := object(t, f.call(t, f.viewer, "GET", url, "", 200, "").Data)
	if detail["status"] != "queued" || detail["execution_actor_id"] != "1" || detail["blocking_database"] != false ||
		detail["next_attempt_at"] == nil || detail["finished_at"] != nil || detail["status_url"] != url ||
		detail["history_url"] != "/api/databases/1/history" || fmt.Sprint(detail["allowed_actions"]) != "[]" {
		t.Fatalf("detail job: %v", detail)
	}
	f.call(t, apiSession{}, "GET", url, "", 401, "unauthenticated")
	f.call(t, f.editor, "GET", "/api/databases/2/jobs/"+first["id"].(string), "", 404, "not_found")
	f.call(t, f.viewer, "POST", url+"/resume", "", 403, "forbidden")
	f.call(t, apiSession{cookie: f.editor.cookie}, "POST", url+"/resume", "", 403, "csrf_invalid")
	f.call(t, f.editor, "POST", url+"/resume", `{"sql_up":"SELECT 1"}`, 400, "invalid_input")
	f.call(t, f.editor, "POST", url+"/resume", "", 409, "state_conflict")
}

func TestEnqueueDoesNotCommitCallerTransaction(t *testing.T) {
	f := newQueueFixture(t)
	request := f.approved(t, 1)
	tx, err := f.Platform.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Enqueue(context.Background(), tx, *request, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var jobs int
	var locked bool
	if err := f.Platform.QueryRow(`SELECT (SELECT count(*) FROM migration_jobs),
		(SELECT locked_at IS NOT NULL FROM migrations WHERE id = $1)`, request.MigrationID).Scan(&jobs, &locked); err != nil || jobs != 0 || locked {
		t.Fatalf("rollback caller harus membatalkan enqueue: %d %v %v", jobs, locked, err)
	}
}

func TestWorkerRunsApprovedSnapshotWhileHoldingLocks(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	var platformFree, targetFree bool
	f.executor.run = func(ctx context.Context, task *migration.JobTask) migration.JobResult {
		for db, free := range map[*sql.DB]*bool{f.Platform: &platformFree, f.Target: &targetFree} {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Error(err)
				continue
			}
			if err := tx.QueryRow(`SELECT pg_try_advisory_xact_lock($1)`, task.DatabaseID).Scan(free); err != nil {
				t.Error(err)
			}
			tx.Rollback()
		}
		// Koneksi target khusus dapat dipakai eksekutor.
		if _, err := task.Target.ExecContext(ctx, `SELECT 1`); err != nil {
			t.Error(err)
		}
		return migration.JobResult{Status: migration.JobSuccess}
	}
	worker := f.worker(t)
	if !runOnce(t, worker) || runOnce(t, worker) {
		t.Fatal("harusnya tepat satu job dikerjakan")
	}
	if platformFree || targetFree {
		t.Fatalf("lock tidak dipegang selama eksekusi: platform=%v target=%v", platformFree, targetFree)
	}
	row := f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
	if row.executions != 1 || row.openAttempt != 0 {
		t.Fatalf("history: %+v", row)
	}
	task := f.executor.tasks[0]
	if task.Snapshot.SQLUp != "CREATE TABLE public.queue_demo (id int)" || task.ActorID != 1 || task.Attempt != 1 {
		t.Fatalf("task: %+v", task)
	}
	var free bool
	tx, err := f.Target.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT pg_try_advisory_xact_lock(1)`).Scan(&free); err != nil || !free {
		t.Fatalf("lock target tidak dilepas: %v %v", free, err)
	}
}

func TestWorkersSerializePerDatabaseAndRunDatabasesInParallel(t *testing.T) {
	f := newQueueFixture(t)
	first, second, other := f.enqueue(t, 1), f.enqueue(t, 1), f.enqueue(t, 2)
	started, release := make(chan int64, 3), make(chan struct{})
	f.executor.run = func(_ context.Context, task *migration.JobTask) migration.JobResult {
		started <- task.JobID
		<-release
		return migration.JobResult{Status: migration.JobFailed, ErrorCode: migration.JobErrorSQL, ErrorMessage: "selesai"}
	}
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		worker := f.worker(t)
		go func() {
			defer group.Done()
			if _, err := worker.RunOnce(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	running := map[int64]bool{waitFor(t, started): true, waitFor(t, started): true}
	if !running[first.ID] || !running[other.ID] {
		t.Errorf("harusnya job pertama tiap database yang paralel: %v", running)
	}
	if ran, err := f.worker(t).RunOnce(context.Background()); ran || err != nil {
		t.Errorf("worker ketiga tidak boleh mengambil job database yang sibuk: %v %v", ran, err)
	}
	if got := f.row(t, second.ID).status; got != migration.JobQueued {
		t.Errorf("job kedua harus menunggu: %s", got)
	}
	close(release)
	group.Wait()
}

func TestDuplicateDeliveryExecutesOnce(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	var group sync.WaitGroup
	for range 6 {
		group.Add(1)
		worker := f.worker(t)
		go func() {
			defer group.Done()
			if _, err := worker.RunOnce(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if f.executor.calls() != 1 {
		t.Fatalf("eksekutor dipanggil %d kali", f.executor.calls())
	}
	f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
}

func TestBusyLockRequeuesWithoutAttempt(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	for name, db := range map[string]*sql.DB{"platform": f.Platform, "target": f.Target} {
		testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET next_attempt_at = now()`)
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(1)`); err != nil {
			t.Fatal(err)
		}
		if runOnce(t, f.worker(t)) {
			t.Fatalf("%s: lock sibuk dianggap eksekusi", name)
		}
		row := f.expectJob(t, job.ID, migration.JobQueued, "", 0)
		if row.auto != 0 || row.executions != 0 {
			t.Fatalf("%s: kuota atau history terpakai: %+v", name, row)
		}
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(1)`); err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
}

func TestTransientFailureRetriesThreeTimesThenManualResume(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	f.executor.run = func(context.Context, *migration.JobTask) migration.JobResult {
		return migration.JobResult{Status: migration.JobFailed, ErrorCode: migration.JobErrorTransient,
			ErrorMessage: "deadlock", Retryable: true}
	}
	worker := f.worker(t)
	for range 3 {
		testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET next_attempt_at = now() WHERE status = 'queued'`)
		runOnce(t, worker)
	}
	row := f.expectJob(t, job.ID, migration.JobFailed, migration.JobErrorTransient, 3)
	if row.executions != 3 || row.openAttempt != 0 || runOnce(t, worker) {
		t.Fatalf("batas retry: %+v", row)
	}

	url := fmt.Sprintf("/api/databases/1/jobs/%d", job.ID)
	detail := object(t, f.call(t, f.editor, "GET", url, "", 200, "").Data)
	if fmt.Sprint(detail["allowed_actions"]) != "[resume]" {
		t.Fatalf("aksi: %v", detail["allowed_actions"])
	}
	resumed := object(t, f.call(t, f.editor, "POST", url+"/resume", "", 202, "").Data)
	if resumed["status"] != "queued" || resumed["id"] != strconv.FormatInt(job.ID, 10) || resumed["error"] != nil {
		t.Fatalf("resume: %v", resumed)
	}
	f.call(t, f.editor, "POST", url+"/resume", "", 409, "state_conflict")
	runOnce(t, worker)
	// resume menambah tepat satu attempt tanpa mengembalikan kuota otomatis
	f.expectJob(t, job.ID, migration.JobFailed, migration.JobErrorTransient, 4)
}

func TestPermanentSQLErrorIsNotRetriedOrResumed(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	f.executor.run = func(context.Context, *migration.JobTask) migration.JobResult {
		return migration.JobResult{Status: migration.JobFailed, ErrorCode: migration.JobErrorSQL, ErrorMessage: "syntax"}
	}
	runOnce(t, f.worker(t))
	f.expectJob(t, job.ID, migration.JobFailed, migration.JobErrorSQL, 1)
	f.call(t, f.editor, "POST", fmt.Sprintf("/api/databases/1/jobs/%d/resume", job.ID), "", 409, "state_conflict")
}

func TestRevalidationRejectsBeforeAnySQL(t *testing.T) {
	for _, test := range []struct{ name, change, code string }{
		{"pelaksana kehilangan Edit", `UPDATE group_database_access SET access_level='view' WHERE group_id=1`, "forbidden"},
		{"pelaksana nonaktif", `UPDATE users SET is_active=false WHERE id=1`, "forbidden"},
		// izin database dianggap hilang untuk koneksi nonaktif, sama seperti di API
		{"koneksi nonaktif", `UPDATE database_connections SET is_active=false WHERE id=1`, "forbidden"},
		{"approval hilang", `UPDATE migration_approvals SET approved_by=NULL, approved_at=NULL WHERE group_id=2`, "approval_incomplete"},
		{"snapshot berbeda", `UPDATE migration_requests SET definition_snapshot=jsonb_set(definition_snapshot,'{sql_up}','"DROP TABLE x"')`, "state_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newQueueFixture(t)
			job := f.enqueue(t, 1)
			testdb.Exec(t, f.Platform, test.change)
			runOnce(t, f.worker(t))
			row := f.expectJob(t, job.ID, migration.JobFailed, test.code, 0)
			if row.executions != 0 || f.executor.calls() != 0 {
				t.Fatalf("SQL tidak boleh berjalan: %+v", row)
			}
		})
	}
}

func TestUnavailableTargetRetriesWithLimit(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	f.config.Targets = func(context.Context, int64) (*sql.DB, error) { return nil, errors.New("target mati") }
	worker := f.worker(t)
	for range 3 {
		testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET next_attempt_at = now() WHERE status = 'queued'`)
		runOnce(t, worker)
	}
	row := f.expectJob(t, job.ID, migration.JobFailed, migration.JobErrorTargetUnavailable, 0)
	if row.auto != 3 || row.executions != 0 {
		t.Fatalf("batas percobaan koneksi: %+v", row)
	}
}

func TestStaleWorkerCannotFinalizeAfterTakeover(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	f.executor.run = func(context.Context, *migration.JobTask) migration.JobResult {
		testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET worker_token = 'pengganti' WHERE id = $1`, job.ID)
		return migration.JobResult{Status: migration.JobSuccess}
	}
	runOnce(t, f.worker(t))
	row := f.expectJob(t, job.ID, migration.JobRunning, "", 1)
	if row.openAttempt != 1 {
		t.Fatalf("worker basi tidak boleh memfinalisasi history: %+v", row)
	}
}

// abandon meniru worker yang mati: job tetap running dengan heartbeat basi.
func (f *queueFixture) abandon(t *testing.T, jobID int64, sqlSent bool) {
	t.Helper()
	testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET status='running', worker_token='mati', started_at=now(),
		heartbeat_at=now() - interval '1 hour' WHERE id=$1`, jobID)
	if !sqlSent {
		return
	}
	testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET attempt_count=1, auto_attempt_count=1 WHERE id=$1`, jobID)
	testdb.Exec(t, f.Platform, `INSERT INTO migration_executions
		(migration_id, executed_by, sql_snapshot, status, job_id, request_id, database_id, revision, operation, attempt)
		SELECT migration_id, execution_actor_id, 'x', 'running', id, request_id, database_id, revision, operation, 1
		FROM migration_jobs WHERE id=$1`, jobID)
}

func (f *queueFixture) recoverJobs(t *testing.T, want int) {
	t.Helper()
	got, err := f.worker(t).RecoverOnce(context.Background())
	if err != nil || got != want {
		t.Fatalf("recovery: %d %v, harusnya %d", got, err, want)
	}
}

func TestRecoveryRequeuesWhenSQLWasNeverSent(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	f.abandon(t, job.ID, false)
	f.recoverJobs(t, 1)
	f.expectJob(t, job.ID, migration.JobQueued, migration.JobErrorInterrupted, 0)
	if len(f.executor.reconcile) != 0 {
		t.Fatal("tanpa row running tidak perlu membaca ledger")
	}
	time.Sleep(5 * time.Millisecond)
	f.drain(t, f.worker(t), job.ID)
	f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
}

func TestRecoveryWaitsForLockInsteadOfStealingByTime(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	f.abandon(t, job.ID, true)
	conn, err := f.Platform.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(1)`); err != nil {
		t.Fatal(err)
	}
	f.recoverJobs(t, 0)
	f.expectJob(t, job.ID, migration.JobRunning, "", 1)
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(1)`); err != nil {
		t.Fatal(err)
	}
	f.executor.verify = func(context.Context, *migration.JobTask) migration.JobResult {
		return migration.JobResult{Status: migration.JobSuccess}
	}
	f.recoverJobs(t, 1)
	row := f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
	if row.openAttempt != 0 || f.executor.calls() != 0 || len(f.executor.reconcile) != 1 || f.executor.reconcile[0].Attempt != 1 {
		t.Fatalf("recovery tidak boleh menjalankan ulang SQL: %+v", row)
	}
}

func TestOutcomeUnknownBlocksDatabaseUntilReconciled(t *testing.T) {
	f := newQueueFixture(t)
	job, waiting := f.enqueue(t, 1), f.enqueue(t, 1)
	f.executor.run = func(context.Context, *migration.JobTask) migration.JobResult {
		return migration.ClassifyExecutionError(errors.New("koneksi putus"), true)
	}
	worker := f.worker(t)
	runOnce(t, worker)
	row := f.expectJob(t, job.ID, migration.JobOutcomeUnknown, migration.JobErrorOutcomeUnknown, 1)
	if row.openAttempt != 1 || runOnce(t, worker) {
		t.Fatalf("database harus ditahan: %+v", row)
	}
	f.expectJob(t, waiting.ID, migration.JobQueued, "", 0)
	f.call(t, f.editor, "POST", fmt.Sprintf("/api/databases/1/jobs/%d/resume", job.ID), "", 409, "state_conflict")

	// target belum dapat diperiksa: tetap unknown, tidak ada retry
	targets := f.config.Targets
	f.config.Targets = func(context.Context, int64) (*sql.DB, error) { return nil, errors.New("target mati") }
	testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET next_attempt_at = now() WHERE id = $1`, job.ID)
	f.recoverJobs(t, 1)
	f.expectJob(t, job.ID, migration.JobOutcomeUnknown, migration.JobErrorTargetUnavailable, 1)

	f.config.Targets = targets
	f.executor.verify = func(context.Context, *migration.JobTask) migration.JobResult {
		return migration.JobResult{Status: migration.JobSuccess}
	}
	f.executor.run = nil
	testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET next_attempt_at = now() WHERE id = $1`, job.ID)
	f.recoverJobs(t, 1)
	f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
	if f.executor.calls() != 1 {
		t.Fatal("SQL tidak boleh diulang selama reconciliation")
	}
}

func TestWorkerRunFinishesActiveJobOnShutdown(t *testing.T) {
	f := newQueueFixture(t)
	job := f.enqueue(t, 1)
	started, release := make(chan int64, 1), make(chan struct{})
	f.executor.run = func(ctx context.Context, task *migration.JobTask) migration.JobResult {
		started <- task.JobID
		<-release
		if ctx.Err() != nil {
			t.Error("job dibatalkan sebelum grace period habis")
		}
		return migration.JobResult{Status: migration.JobSuccess}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.worker(t).Run(ctx); close(done) }()
	waitFor(t, started)
	cancel()
	late := f.enqueue(t, 2)
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker tidak berhenti")
	}
	f.expectJob(t, job.ID, migration.JobSuccess, "", 1)
	f.expectJob(t, late.ID, migration.JobQueued, "", 0)
}

func TestCompensationEnqueueClaimsSourceJobOnce(t *testing.T) {
	f := newQueueFixture(t)
	source := f.enqueue(t, 1)
	f.executor.run = func(context.Context, *migration.JobTask) migration.JobResult {
		testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET committed_batches=2 WHERE id=$1`, source.ID)
		return migration.JobResult{Status: migration.JobPartial, ErrorCode: migration.JobErrorSQL, ErrorMessage: "batch 3 gagal"}
	}
	runOnce(t, f.worker(t))
	url := fmt.Sprintf("/api/databases/1/jobs/%d", source.ID)
	detail := object(t, f.call(t, f.editor, "GET", url, "", 200, "").Data)
	if detail["blocking_database"] != true || fmt.Sprint(detail["allowed_actions"]) != "[resume request_compensate]" {
		t.Fatalf("job partial: %v", detail)
	}

	approve := func() *migration.MigrationRequest {
		ctx := context.Background()
		request, err := f.workflow.Create(ctx, f.Editor, 1, migration.RequestInput{
			MigrationID: source.MigrationID, Revision: 1, Operation: "compensate", SourceJobID: &source.ID})
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
		return request
	}
	first, second := approve(), approve()
	job, err := f.workflow.Execute(context.Background(), f.Editor, 1, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	reply := f.call(t, f.editor, "POST", fmt.Sprintf("/api/databases/1/requests/%d/execute", second.ID), "", 409, "state_conflict")
	if reply.Error.Details["existing_job_id"] != strconv.FormatInt(job.ID, 10) {
		t.Fatalf("details: %v", reply.Error.Details)
	}
	// setelah kompensasi antre, job sumber tidak boleh dilanjutkan
	f.call(t, f.editor, "POST", url+"/resume", "", 409, "state_conflict")

	// kompensasi yang gagal permanen tetap bisa dilanjutkan agar database tidak tertahan selamanya
	f.executor.run = func(context.Context, *migration.JobTask) migration.JobResult {
		return migration.JobResult{Status: migration.JobFailed, ErrorCode: migration.JobErrorSQL, ErrorMessage: "down gagal"}
	}
	worker := f.worker(t)
	runOnce(t, worker)
	f.expectJob(t, job.ID, migration.JobFailed, migration.JobErrorSQL, 1)
	f.call(t, f.editor, "POST", fmt.Sprintf("/api/databases/1/jobs/%d/resume", job.ID), "", 202, "")
	f.executor.run = nil
	runOnce(t, worker)
	f.expectJob(t, job.ID, migration.JobSuccess, "", 2)
}
