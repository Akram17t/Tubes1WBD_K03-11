package migration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"tubes-wbd/src/migration"
	testdb "tubes-wbd/test/database"
)

// newWorkflowFixture menyiapkan API bersesi dan request draft dengan validator definisi nyata.
func newWorkflowFixture(t *testing.T) (*definitionFixture, string) {
	t.Helper()
	f := newDefinitionFixture(t)
	testdb.Exec(t, f.Platform, `UPDATE groups SET leader_user_id=id`)
	definition := object(t, f.call(t, f.editor, "POST", base, normalBody, 201, "").Data)
	body := fmt.Sprintf(`{"migration_id":%q,"revision":1,"operation":"up"}`, definition["id"])
	request := object(t, f.call(t, f.editor, "POST", "/api/databases/1/requests", body, 201, "").Data)
	return f, "/api/databases/1/requests/" + request["id"].(string)
}

// workflowDetail membaca detail request melalui API dan memeriksa bentuk JSON-nya.
func workflowDetail(t *testing.T, f *definitionFixture, session apiSession, path string) migration.RequestDetail {
	t.Helper()
	var detail migration.RequestDetail
	reply := f.call(t, session, "GET", path, "", 200, "")
	if err := json.Unmarshal(reply.Data, &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

// TestWorkflowAPIApprovalBeforeExecute memeriksa approval parsial dan batas izin leader View.
func TestWorkflowAPIApprovalBeforeExecute(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	f.call(t, f.editor, "POST", path+"/approve", "", 200, "") // pengaju boleh menyetujui untuk group-nya
	detail := workflowDetail(t, f, f.viewer, path)
	if detail.ReviewStatus != "pending_review" || !reflect.DeepEqual(detail.PendingGroupIDs, []string{"2"}) ||
		!detail.CanApprove || !slices.Contains(detail.AllowedActions, "approve") {
		t.Fatalf("approval parsial dan aksi leader View: %+v", detail)
	}
	f.call(t, f.editor, "POST", path+"/execute", "", 409, "approval_incomplete")
	f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
	detail = workflowDetail(t, f, f.viewer, path)
	if detail.ReviewStatus != "approved" || len(detail.PendingGroupIDs) != 0 || detail.CanApprove ||
		slices.Contains(detail.AllowedActions, "execute") {
		t.Fatalf("View boleh approve tetapi tidak boleh execute: %+v", detail)
	}
	f.call(t, f.viewer, "POST", path+"/execute", "", 403, "forbidden")
	// Fixture tidak memasang enqueue; approval tidak boleh diam-diam menjalankan SQL.
	f.call(t, f.editor, "POST", path+"/execute", "", 503, "workflow_unavailable")
	var jobs, executions int
	if err := f.Platform.QueryRow(`SELECT (SELECT count(*) FROM migration_jobs),
		(SELECT count(*) FROM migration_executions)`).Scan(&jobs, &executions); err != nil || jobs != 0 || executions != 0 {
		t.Fatalf("review tidak boleh menjalankan migration: jobs=%d executions=%d err=%v", jobs, executions, err)
	}
}

// TestWorkflowOneLeaderApprovesAllRepresentedGroups memastikan satu klik mengisi semua group yang dipimpin.
func TestWorkflowOneLeaderApprovesAllRepresentedGroups(t *testing.T) {
	f, path := newWorkflowFixture(t)
	testdb.Exec(t, f.Platform, `INSERT INTO user_groups(user_id,group_id) VALUES (1,2);
		UPDATE groups SET leader_user_id=1 WHERE id=2`)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	f.call(t, f.viewer, "POST", path+"/approve", "", 403, "forbidden")
	reply := f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
	var approval migration.ApprovalResult
	if err := json.Unmarshal(reply.Data, &approval); err != nil {
		t.Fatal(err)
	}
	slices.Sort(approval.ApprovedGroupIDs)
	if approval.ReviewStatus != "approved" || !reflect.DeepEqual(approval.ApprovedGroupIDs, []string{"1", "2"}) {
		t.Fatalf("satu klik harus mengisi dua group: %+v", approval)
	}
	detail := workflowDetail(t, f, f.editor, path)
	if len(detail.Approvals) != 2 {
		t.Fatalf("slot approval harus per group: %+v", detail.Approvals)
	}
	for _, slot := range detail.Approvals {
		if slot.ApprovedBy == nil || *slot.ApprovedBy != 1 || slot.ApprovedAt == nil {
			t.Fatalf("group belum ditandatangani: %+v", slot)
		}
	}
}

// TestWorkflowMissingLeaderWaitsForReplacement memastikan group tanpa leader tetap menahan approval.
func TestWorkflowMissingLeaderWaitsForReplacement(t *testing.T) {
	f, path := newWorkflowFixture(t)
	testdb.Exec(t, f.Platform, `UPDATE groups SET leader_user_id=NULL WHERE id=2`)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
	detail := workflowDetail(t, f, f.viewer, path)
	if detail.ReviewStatus != "pending_review" || len(detail.Approvals) != 2 ||
		!detail.Approvals[1].WaitingForLeader || detail.CanApprove ||
		!reflect.DeepEqual(detail.PendingGroupIDs, []string{"2"}) {
		t.Fatalf("group tanpa leader tidak boleh diabaikan: %+v", detail)
	}
	f.call(t, f.viewer, "POST", path+"/approve", "", 403, "forbidden")
	f.call(t, f.editor, "POST", path+"/execute", "", 409, "approval_incomplete")
	testdb.Exec(t, f.Platform, `UPDATE groups SET leader_user_id=2 WHERE id=2`)
	detail = workflowDetail(t, f, f.viewer, path)
	if detail.Approvals[1].WaitingForLeader || !detail.CanApprove {
		t.Fatalf("leader pengganti harus bisa mengisi slot lama: %+v", detail)
	}
	f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
	if detail := workflowDetail(t, f, f.editor, path); detail.ReviewStatus != "approved" {
		t.Fatalf("request harus approved setelah leader pengganti menyetujui: %+v", detail)
	}
}

// TestWorkflowDuplicateApprovePreservesSignatures memastikan approve berulang tidak menimpa tanda tangan lama.
func TestWorkflowDuplicateApprovePreservesSignatures(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
	before := workflowDetail(t, f, f.editor, path)
	// reviewer non-admin tetap bisa liat nama pengaju dan penanda tangan
	if before.RequestedByName == "" || before.Approvals[0].ApprovedByName == nil || *before.Approvals[0].ApprovedByName == "" {
		t.Fatalf("nama pengaju/approver harus dikirim: %+v", before)
	}
	for _, state := range []string{"pending_review", "approved"} {
		if state == "approved" {
			f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
		}
		reply := f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
		approved := object(t, reply.Data)
		if approved["review_status"] != state || fmt.Sprint(approved["approved_group_ids"]) != "[]" {
			t.Fatalf("approve ulang tidak boleh menambah tanda tangan: %s", reply.Data)
		}
		after := workflowDetail(t, f, f.editor, path)
		if len(after.Approvals) != 2 || !reflect.DeepEqual(before.Approvals[0], after.Approvals[0]) {
			t.Fatalf("tanda tangan lama berubah: before=%+v after=%+v", before.Approvals, after.Approvals)
		}
	}
}

// TestWorkflowCancelBeforeEnqueue memeriksa penarikan request oleh pengaju sebelum job dibuat.
func TestWorkflowCancelBeforeEnqueue(t *testing.T) {
	for _, state := range []string{"draft", "pending_review", "approved"} {
		t.Run(state, func(t *testing.T) {
			f, path := newWorkflowFixture(t)
			// User lain juga memiliki Edit, tetapi bukan pengaju request ini.
			testdb.Exec(t, f.Platform, `INSERT INTO user_groups(user_id,group_id) VALUES (2,1)`)
			if state != "draft" {
				f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
			}
			if state == "approved" {
				f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
				f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
			}
			before := workflowDetail(t, f, f.editor, path)
			f.call(t, f.viewer, "POST", path+"/cancel", "", 403, "forbidden")
			if got := workflowDetail(t, f, f.editor, path); got.ReviewStatus != state {
				t.Fatalf("cancel user lain mengubah status: %+v", got)
			}
			f.call(t, f.editor, "POST", path+"/cancel", "", 200, "")
			f.call(t, f.editor, "POST", path+"/cancel", "", 200, "")
			after := workflowDetail(t, f, f.editor, path)
			if after.ReviewStatus != "cancelled" || len(after.AllowedActions) != 0 ||
				!reflect.DeepEqual(before.Snapshot, after.Snapshot) || len(before.Approvals) != len(after.Approvals) {
				t.Fatalf("cancel harus menyimpan snapshot/history dan menutup aksi: %+v", after)
			}
			f.call(t, f.editor, "POST", path+"/submit", "", 409, "state_conflict")
			f.call(t, f.editor, "POST", path+"/approve", "", 409, "state_conflict")
			f.call(t, f.editor, "POST", path+"/execute", "", 409, "approval_incomplete")
		})
	}
}

// TestWorkflowCannotCancelPersistedJob memastikan request yang sudah punya job tidak dapat ditarik.
func TestWorkflowCannotCancelPersistedJob(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
	f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
	before := workflowDetail(t, f, f.editor, path)
	// Simulasikan job tersimpan tanpa menggandakan implementasi enqueue.
	testdb.Exec(t, f.Platform, `INSERT INTO migration_jobs
		(request_id,database_id,migration_id,revision,operation,execution_actor_id)
		VALUES ($1,1,$2,1,'up',1)`, before.ID, before.MigrationID)
	f.call(t, f.editor, "POST", path+"/cancel", "", 409, "state_conflict")
	after := workflowDetail(t, f, f.editor, path)
	if after.ReviewStatus != "approved" || after.WorkflowStatus != "queued" || after.JobID == nil ||
		slices.Contains(after.AllowedActions, "cancel") {
		t.Fatalf("cancel tidak boleh menarik request yang sudah punya job: %+v", after)
	}
}

// TestWorkflowRejectsCrossDatabaseIDs memastikan ID dari database lain tidak membocorkan atau mengubah request.
func TestWorkflowRejectsCrossDatabaseIDs(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	before := workflowDetail(t, f, f.editor, path)
	otherPath := fmt.Sprintf("/api/databases/2/requests/%d", before.ID)
	// Editor punya akses ke kedua database: penolakan harus karena parent ID salah.
	f.call(t, f.editor, "GET", otherPath, "", 404, "not_found")
	for _, action := range []string{"submit", "approve", "cancel", "execute"} {
		f.call(t, f.editor, "POST", otherPath+"/"+action, "", 404, "not_found")
	}
	body := fmt.Sprintf(`{"migration_id":"%d","revision":1,"operation":"up"}`, before.MigrationID)
	f.call(t, f.editor, "POST", "/api/databases/2/requests", body, 404, "not_found")
	listed := f.call(t, f.editor, "GET", "/api/databases/2/requests", "", 200, "")
	if string(listed.Data) != "[]" || !reflect.DeepEqual(before, workflowDetail(t, f, f.editor, path)) {
		t.Fatalf("ID lintas database membocorkan/mengubah request: %s", listed.Data)
	}
}

// TestWorkflowRejectsStaleRevision memastikan revisi basi ditolak tanpa mengubah snapshot atau slot approval.
func TestWorkflowRejectsStaleRevision(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, f.editor, "POST", "/api/databases/1/requests",
		`{"migration_id":"1","revision":2,"operation":"up"}`, 409, "state_conflict")
	f.call(t, f.editor, "PATCH", base+"/1", `{"revision":1,"title":"Revisi baru"}`, 200, "")
	f.call(t, f.editor, "POST", path+"/submit", "", 409, "state_conflict")
	detail := workflowDetail(t, f, f.editor, path)
	if detail.ReviewStatus != "draft" || detail.Revision != 1 || detail.Snapshot.Title != "Tambah nickname" ||
		len(detail.Approvals) != 0 || detail.SubmittedAt != nil {
		t.Fatalf("submit gagal tidak boleh mengubah snapshot/slot/status: %+v", detail)
	}
}

// TestWorkflowApprovalCheckRejectsWrongRevision memeriksa revisi approved tanpa menutup transaction caller.
func TestWorkflowApprovalCheckRejectsWrongRevision(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
	f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
	detail := workflowDetail(t, f, f.editor, path)
	tx, err := f.Platform.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.Background()
	if _, err := migration.CheckRequestApproval(ctx, tx, f.Editor, 1, detail.ID, 2); !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("worker wajib memakai revisi approved: %v", err)
	}
	if _, err := migration.CheckRequestApproval(ctx, tx, f.Editor, 1, detail.ID, 1); err != nil {
		t.Fatalf("revisi benar harus diterima dalam transaction caller yang masih terbuka: %v", err)
	}
}

// TestWorkflowAPIRequiresSessionAndCSRF memastikan mutasi ditolak tanpa session atau token CSRF yang valid.
func TestWorkflowAPIRequiresSessionAndCSRF(t *testing.T) {
	f, path := newWorkflowFixture(t)
	f.call(t, apiSession{}, "GET", path, "", 401, "unauthenticated")
	for _, target := range []string{"/api/databases/1/requests", path + "/submit", path + "/approve", path + "/cancel", path + "/execute"} {
		f.call(t, apiSession{}, "POST", target, "", 401, "unauthenticated")
		f.call(t, apiSession{cookie: f.editor.cookie}, "POST", target, "", 403, "csrf_invalid")
	}
	detail := workflowDetail(t, f, f.editor, path)
	if detail.ReviewStatus != "draft" || len(detail.Approvals) != 0 {
		t.Fatalf("request tanpa session/CSRF mengubah state: %+v", detail)
	}
}
