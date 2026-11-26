package migration_test

import (
	"fmt"
	"reflect"
	"testing"

	testdb "tubes-wbd/test/database"
)

// TestWorkflowApprovalErrorMatchesSPAPendingGroups memeriksa ID pending yang dipetakan SPA ke nama group.
func TestWorkflowApprovalErrorMatchesSPAPendingGroups(t *testing.T) {
	f, path := newWorkflowFixture(t)
	check := func(want []string) {
		t.Helper()
		reply := f.call(t, f.editor, "POST", path+"/execute", "", 409, "approval_incomplete")
		ids, ok := reply.Error.Details["pending_group_ids"].([]any)
		if !ok {
			t.Fatalf("pending_group_ids harus array, bukan null/missing: %+v", reply.Error)
		}
		got := []string{}
		for _, id := range ids {
			text, ok := id.(string)
			if !ok {
				t.Fatalf("BIGINT harus berupa string untuk SPA: %v", id)
			}
			got = append(got, text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pending IDs: got %v want %v", got, want)
		}
		detail := workflowDetail(t, f, f.editor, path)
		if !reflect.DeepEqual(got, detail.PendingGroupIDs) {
			t.Fatalf("error execute dan detail tidak konsisten: %v / %v", got, detail.PendingGroupIDs)
		}
		for _, id := range got {
			found := false
			for _, slot := range detail.Approvals {
				if fmt.Sprint(slot.GroupID) == id && slot.GroupName != "" && slot.ApprovedBy == nil {
					found = true
				}
			}
			if !found {
				t.Fatalf("SPA tidak bisa memetakan ID %s ke nama slot pending", id)
			}
		}
	}
	check([]string{}) // draft belum punya slot approval, tetapi execute tetap harus ditolak
	testdb.Exec(t, f.Platform, `UPDATE groups SET leader_user_id=NULL WHERE id=2`)
	f.call(t, f.editor, "POST", path+"/submit", "", 200, "")
	check([]string{"1", "2"})
	// Group yang ditambahkan setelah submit tidak ikut snapshot approval request ini.
	testdb.Exec(t, f.Platform, `INSERT INTO groups(id,name) VALUES (3,'New group');
		INSERT INTO group_database_access(group_id,database_id,access_level,granted_by) VALUES (3,1,'view',4)`)
	f.call(t, f.editor, "POST", path+"/approve", "", 200, "")
	check([]string{"2"}) // group tanpa leader juga harus muncul di detail error
	denied := f.call(t, f.viewer, "POST", path+"/execute", "", 403, "forbidden")
	if denied.Error.Details != nil {
		t.Fatalf("penolakan izin tidak boleh membocorkan daftar approval: %+v", denied.Error)
	}
	testdb.Exec(t, f.Platform, `UPDATE groups SET leader_user_id=2 WHERE id=2`)
	f.call(t, f.viewer, "POST", path+"/approve", "", 200, "")
	// Approval sudah lengkap; fixture tetap menolak eksekusi karena callback enqueue tidak dipasang.
	f.call(t, f.editor, "POST", path+"/execute", "", 503, "workflow_unavailable")
}
