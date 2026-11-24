package migration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"tubes-wbd/src/database"
	"tubes-wbd/src/migration"
	"tubes-wbd/src/telemetry"
	testdb "tubes-wbd/test/database"

	"github.com/gin-gonic/gin"
)

// historyFixture menyimpan timeline contoh spesifikasi, kegagalan, dan kompensasi tanpa menjalankan worker.
func historyFixture(t *testing.T) (*testdb.Fixture, *gin.Engine) {
	t.Helper()
	f := testdb.New(t)
	testdb.Exec(t, f.Platform, `
		INSERT INTO migrations (id,database_id,title,sql_content,created_by) VALUES
		(1,1,'A','SQL definisi sekarang',1),(2,1,'B','SQL definisi sekarang',1),
		(3,1,'C','SQL definisi sekarang',1),(4,1,'Batch','SQL definisi sekarang',1),
		(5,2,'Database lain','SELECT 90',1);
		INSERT INTO migration_requests (id,database_id,migration_id,revision,operation,requested_by,definition_snapshot)
		VALUES (81,1,4,1,'up',1,'{}');
		INSERT INTO migration_jobs (id,request_id,database_id,migration_id,revision,operation,execution_actor_id)
		VALUES (81,81,1,4,1,'up',1);
		INSERT INTO migration_requests (id,database_id,migration_id,revision,operation,source_job_id,requested_by,definition_snapshot)
		VALUES (82,1,4,1,'compensate',81,1,'{}');
		INSERT INTO migration_jobs (id,request_id,database_id,migration_id,revision,operation,execution_actor_id)
		VALUES (82,82,1,4,1,'compensate',1);
		INSERT INTO migration_executions
		(id,migration_id,executed_by,sql_snapshot,status,operation,target_execution_id,
		 started_at,finished_at) VALUES
		(1,1,1,'SQL A asli','success','up',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(2,2,1,'SQL B asli','success','up',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(3,2,1,'SQL down B asli','success','rollback',2,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(4,2,1,'SQL B asli','success','forward',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(5,3,1,'SQL C asli','success','up',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(6,2,1,'SQL down B asli','failed','rollback',4,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(7,2,1,'SELECT 1/0','failed','up',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(8,4,1,'UPDATE demo SET value=value+1 WHERE id=ANY($1)','partial','up',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(9,4,1,'UPDATE demo SET value=value-1 WHERE id=ANY($1)','partially_reverted','compensate',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(10,4,1,'UPDATE demo SET value=value-1 WHERE id=ANY($1)','success','compensate',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(90,5,1,'SELECT 90','success','up',NULL,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z');
		UPDATE migration_executions SET request_id=81,job_id=81,attempt=2,
		 total_rows=3,committed_rows=2,committed_batches=1,last_cursor=9007199254740993
		 WHERE id=8;
		UPDATE migration_executions SET request_id=82,job_id=82,attempt=id-8 WHERE id IN (9,10);
		UPDATE migration_executions SET error_message='pembagian nol',sqlstate='22012' WHERE id=7;
		INSERT INTO migration_batch_attempts
		(execution_id,job_id,batch_number,direction,first_id,last_id,sql_snapshot,parameters,status,row_count,started_at,finished_at)
		VALUES (8,81,1,'up',1,9007199254740993,'SQL batch 1','{"ids":2}',
		 'success',2,'2026-11-24 00:00:00Z','2026-11-24 00:00:01Z'),
		(8,81,2,'up',3,3,'SQL batch 2','{"ids":1}',
		 'failed',0,'2026-11-24 00:00:02Z','2026-11-24 00:00:03Z');
	`)
	r := f.Router(t)
	r.Use(telemetry.RequestIDMiddleware())
	migration.RegisterRoutes(r, f.Platform, f.Provider)
	return f, r
}

// TestPersistedJobStateReachesWorkflowAndViewer memastikan placeholder lama memakai data queue yang nyata.
func TestPersistedJobStateReachesWorkflowAndViewer(t *testing.T) {
	f, _ := historyFixture(t)
	testdb.Exec(t, f.Platform, `UPDATE migration_requests SET review_status='approved',submitted_at=now() WHERE id=81;
		UPDATE migration_jobs SET status='partial',attempt_count=2,committed_rows=2,committed_batches=1 WHERE id=81;
		UPDATE migration_jobs SET status='running',started_at=now(),worker_token='test-worker' WHERE id=82`)
	ctx := context.Background()
	service := migration.WorkflowService{DB: f.Platform, FindJob: migration.FindRequestJob}
	detail, err := service.Get(ctx, f.Viewer, 1, 81)
	if err != nil || detail.JobID == nil || *detail.JobID != 81 || detail.WorkflowStatus != "partial" || detail.ReviewStatus != "approved" {
		t.Fatalf("status workflow harus berasal dari job tanpa menimpa review: %+v, %v", detail, err)
	}
	summary, err := database.GetDatabase(ctx, f.Platform, f.Viewer, 1)
	if err != nil || summary.ActiveJobID == nil || *summary.ActiveJobID != 82 || summary.BlockingJobID == nil || *summary.BlockingJobID != 81 || summary.BlockReason == nil || *summary.BlockReason != "partial" {
		t.Fatalf("viewer harus memperlihatkan job aktif dan blocker: %+v, %v", summary, err)
	}
	databases, err := database.GetDatabases(ctx, f.Platform, f.Viewer)
	if err != nil || len(databases) != 1 || databases[0].BlockingJobID == nil || *databases[0].BlockingJobID != 81 {
		t.Fatalf("daftar database harus membawa blocker yang sama: %+v, %v", databases, err)
	}
	tx, err := f.Platform.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	job, err := migration.FindRequestJob(ctx, tx, 81)
	if err != nil || job == nil || !job.BlockingDatabase || job.Progress.CommittedRows != 2 {
		t.Fatalf("progress job tidak terbaca: %+v, %v", job, err)
	}
	missing, err := migration.FindRequestJob(ctx, tx, 999)
	if err != nil || missing != nil {
		t.Fatalf("request tanpa job harus nil: %+v, %v", missing, err)
	}
}

// TestHistoryHidesOnlySuccessfulExecutionPairs menguji pasangan, urutan stabil, dan pagination setelah filter.
func TestHistoryHidesOnlySuccessfulExecutionPairs(t *testing.T) {
	f, r := historyFixture(t)
	viewer := testdb.Login(t, r, "viewer")
	for _, tc := range []struct {
		query   string
		ids     []int64
		hasMore bool
	}{
		{"", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, false},
		{"hide_rollback=true", []int64{1, 4, 5, 6, 7, 8, 9, 10}, false},
		{"hide_rollback=true&limit=2&offset=1", []int64{4, 5}, true},
		{"hide_rollback=true&limit=2&offset=6", []int64{9, 10}, false},
		{"hide_rollback=true&offset=50", []int64{}, false},
	} {
		w := testdb.Request(r, http.MethodGet, "/api/databases/1/history?"+tc.query, nil, viewer)
		var body struct {
			Data []migration.ExecutionResponse
			Meta struct {
				HasMore   bool   `json:"has_more"`
				RequestID string `json:"request_id"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
			t.Fatalf("history %s: %d %s, %v", tc.query, w.Code, w.Body.String(), err)
		}
		ids := []int64{}
		for _, execution := range body.Data {
			ids = append(ids, execution.ID)
			if execution.ID == 4 && execution.SQLSnapshot != "SQL B asli" {
				t.Fatal("history harus membaca snapshot execution, bukan SQL definisi")
			}
		}
		if !reflect.DeepEqual(ids, tc.ids) || body.Meta.HasMore != tc.hasMore || body.Data == nil {
			t.Fatalf("%s: ids=%v has_more=%v, ingin %v %v", tc.query, ids, body.Meta.HasMore, tc.ids, tc.hasMore)
		}
		if body.Meta.RequestID == "" || body.Meta.RequestID != w.Header().Get("X-Request-ID") {
			t.Fatal("history harus memakai request ID middleware")
		}
	}
	// Forward yang kemudian di-rollback ikut tersembunyi; rollback gagal dan kompensasi tetap terlihat.
	testdb.Exec(t, f.Platform, `INSERT INTO migration_executions
		(id,migration_id,executed_by,sql_snapshot,status,operation,target_execution_id,started_at)
		VALUES (11,2,1,'down','success','rollback',4,'2026-11-24 00:00:00Z'),
		(12,2,1,'referensi database lain','success','rollback',90,'2026-11-24 00:00:00Z')`)
	entries, _, err := migration.GetHistory(context.Background(), f.Platform, f.Viewer, 1, true, 50, 0)
	ids := []int64{}
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 5, 6, 7, 8, 9, 10, 12}) {
		t.Fatalf("pasangan forward/cross-database: %v, %v", ids, err)
	}
	var count int
	if err := f.Platform.QueryRow(`SELECT count(*) FROM migration_executions`).Scan(&count); err != nil || count != 13 {
		t.Fatalf("filter tidak boleh mengubah history: count=%d, %v", count, err)
	}
}

// TestHistoryDetailPermissionsAndBatchPagination memastikan snapshot/batch tidak bocor ke database atau user lain.
func TestHistoryDetailPermissionsAndBatchPagination(t *testing.T) {
	f, r := historyFixture(t)
	viewer, editor := testdb.Login(t, r, "viewer"), testdb.Login(t, r, "editor")
	outsider, admin := testdb.Login(t, r, "outsider"), testdb.Login(t, r, "admin")
	for _, tc := range []struct {
		path   string
		cookie *http.Cookie
		status int
	}{
		{"/api/databases/1/history", nil, 401},
		{"/api/databases/1/history", outsider, 403},
		{"/api/databases/1/history", admin, 403},
		{"/api/databases/1/executions/8", outsider, 403},
		{"/api/databases/1/executions/90", editor, 404},
		{"/api/databases/2/executions/1", editor, 404},
		{"/api/databases/1/history?hide_rollback=invalid", viewer, 400},
		{"/api/databases/1/history?limit=0", viewer, 400},
		{"/api/databases/1/history?offset=-1", viewer, 400},
		{"/api/databases/1/executions/8?limit=501", viewer, 400},
		{"/api/databases/1/executions/0", viewer, 404},
		{"/api/databases/2/history", editor, 200},
	} {
		w := testdb.Request(r, http.MethodGet, tc.path, nil, tc.cookie)
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s: %d %s, ingin %d", tc.path, w.Code, w.Body.String(), tc.status)
		}
	}
	for offset := 0; offset < 3; offset++ {
		path := "/api/databases/1/executions/8?limit=1&offset=" + strconv.Itoa(offset)
		w := testdb.Request(r, http.MethodGet, path, nil, viewer)
		var body struct{ Data migration.ExecutionDetail }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
			t.Fatalf("detail: %d %s, %v", w.Code, w.Body.String(), err)
		}
		detail := body.Data
		if detail.Attempt != 2 || detail.Progress.CommittedRows != 2 || detail.BatchAttempts == nil ||
			detail.BatchPage.HasMore != (offset == 0) || detail.BatchPage.Offset != offset {
			t.Fatalf("progress/pagination batch salah: %+v", detail)
		}
		if offset == 0 && (detail.BatchAttempts[0].FirstID == nil || *detail.BatchAttempts[0].FirstID != 1 || detail.BatchAttempts[0].LastID == nil || *detail.BatchAttempts[0].LastID != 9007199254740993) {
			t.Fatalf("ID cakupan harus string utuh: %+v", detail.BatchAttempts[0])
		}
		if offset == 1 && (detail.BatchAttempts[0].Status != "failed" || detail.BatchAttempts[0].RowCount == nil || *detail.BatchAttempts[0].RowCount != 0) {
			t.Fatalf("attempt batch gagal harus dipertahankan: %+v", detail.BatchAttempts[0])
		}
		if offset == 2 && len(detail.BatchAttempts) != 0 {
			t.Fatal("halaman di luar cakupan harus kosong")
		}
		if !strings.Contains(w.Body.String(), `"job_id":"81"`) || strings.Contains(w.Body.String(), `"Valid"`) {
			t.Fatalf("JSON tidak boleh memakai sql.Null*: %s", w.Body.String())
		}
	}
	w := testdb.Request(r, http.MethodGet, "/api/databases/1/executions/7", nil, viewer)
	if !strings.Contains(w.Body.String(), `"sqlstate":"22012"`) || !strings.Contains(w.Body.String(), `"error_message":"pembagian nol"`) {
		t.Fatalf("detail kegagalan hilang: %s", w.Body.String())
	}
	testdb.Exec(t, f.Platform, `UPDATE users SET is_active=false WHERE id=2`)
	if _, _, err := migration.GetHistory(context.Background(), f.Platform, f.Viewer, 1, false, 50, 0); err == nil {
		t.Fatal("akun nonaktif tidak boleh membaca history")
	}
}

// TestHistoryValidatesPaginationBeforeDatabase menolak parameter tidak valid tanpa menyentuh database.
func TestHistoryValidatesPaginationBeforeDatabase(t *testing.T) {
	for _, tc := range []struct {
		databaseID    int64
		limit, offset int
	}{{0, 50, 0}, {1, 0, 0}, {1, 501, 0}, {1, 50, -1}} {
		if _, _, err := migration.GetHistory(context.Background(), nil, nil, tc.databaseID, false, tc.limit, tc.offset); !errors.Is(err, migration.ErrInvalidInput) {
			t.Fatalf("parameter tidak valid harus ditolak: %v", err)
		}
	}
	if _, err := migration.GetExecutionDetail(context.Background(), nil, nil, 1, 0, 50, 0); !errors.Is(err, migration.ErrInvalidInput) {
		t.Fatalf("execution ID tidak valid harus ditolak: %v", err)
	}
}
