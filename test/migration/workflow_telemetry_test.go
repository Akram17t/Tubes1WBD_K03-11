package migration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"tubes-wbd/src/admin"
	"tubes-wbd/src/telemetry"
	testdb "tubes-wbd/test/database"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// captureActivities menampung log dan span selama test lalu mengembalikan logger/tracer semula.
func captureActivities(t *testing.T) (*bytes.Buffer, *tracetest.SpanRecorder) {
	t.Helper()
	var logs bytes.Buffer
	previousLogger, previousTracer := slog.Default(), otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	slog.SetDefault(telemetry.NewLogger(&logs, slog.LevelDebug))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		provider.Shutdown(context.Background())
		slog.SetDefault(previousLogger)
		otel.SetTracerProvider(previousTracer)
	})
	return &logs, recorder
}

// activityEntry mencari satu event request dan mencocokkan status serta ID span-nya.
func activityEntry(t *testing.T, logs *bytes.Buffer, recorder *tracetest.SpanRecorder, event, status, requestID string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["event"] == event && entry["request_id"] == requestID {
			if found != nil {
				t.Fatalf("event service tercatat dua kali: %s", event)
			}
			found = entry
		}
	}
	if found == nil || found["status"] != status || found["actor_id"] == nil || found["duration_ms"] == nil {
		t.Fatalf("hasil %s %s tidak tercatat: %v", event, status, found)
	}
	for _, span := range recorder.Ended() {
		if span.SpanContext().SpanID().String() == found["span_id"] {
			code := codes.Ok
			if status == "error" {
				code = codes.Error
			}
			if span.Name() != event || span.Status().Code != code || span.SpanContext().TraceID().String() != found["trace_id"] {
				t.Fatalf("hasil span dan log tidak cocok: %s %+v", span.Name(), span.Status())
			}
			return found
		}
	}
	t.Fatalf("span untuk log %s tidak ditemukan", event)
	return nil
}

// TestWorkflowActivitiesRecordCommittedAndRejectedActions memeriksa log aksi workflow yang berhasil dan ditolak.
func TestWorkflowActivitiesRecordCommittedAndRejectedActions(t *testing.T) {
	logs, spans := captureActivities(t)
	f, path := newWorkflowFixture(t)
	detail := workflowDetail(t, f, f.editor, path)
	for _, tc := range []struct {
		action, status, code string
		httpStatus           int
	}{
		{"approve", "error", "state_conflict", 409},
		{"submit", "success", "", 200},
		{"approve", "success", "", 200},
		{"cancel", "success", "", 200},
		{"submit", "error", "state_conflict", 409},
	} {
		reply := f.call(t, f.editor, "POST", path+"/"+tc.action, "", tc.httpStatus, tc.code)
		entry := activityEntry(t, logs, spans, "workflow."+tc.action, tc.status, reply.Meta.RequestID)
		if entry["database_id"] != float64(1) || entry["request_entry_id"] != float64(detail.ID) || entry["actor_id"] != "1" {
			t.Fatalf("ID bisnis/actor workflow salah: %v", entry)
		}
		if tc.action == "cancel" && entry["review_status"] != "cancelled" {
			t.Fatalf("status setelah commit harus tercatat: %v", entry)
		}
	}
	// Input enum tidak valid tidak boleh disalin ke log/span.
	reply := f.call(t, f.editor, "POST", "/api/databases/1/requests",
		`{"migration_id":"1","revision":1,"operation":"private-operation"}`, 400, "invalid_input")
	entry := activityEntry(t, logs, spans, "workflow.create", "error", reply.Meta.RequestID)
	if entry["operation"] != "invalid" || strings.Contains(logs.String(), "private-operation") {
		t.Fatalf("input operation mentah bocor: %v", entry)
	}
}

// TestGroupActivitiesRecordCommitAndRollbackWithoutInputText memeriksa hasil perubahan group tanpa membocorkan input.
func TestGroupActivitiesRecordCommitAndRollbackWithoutInputText(t *testing.T) {
	f := newDefinitionFixture(t)
	// Fixture mengisi ID group secara eksplisit, jadi sesuaikan sequence sebelum menguji create.
	testdb.Exec(t, f.Platform, `SELECT setval(pg_get_serial_sequence('groups','id'),2,true)`)
	admin.RegisterRoutes(f.router, f.Platform)
	adminSession := f.login(t, "admin")
	logs, spans := captureActivities(t)
	created := f.call(t, adminSession, "POST", "/api/admin/groups",
		`{"name":"private-group-name","description":"private-description"}`, 201, "")
	group := object(t, created.Data)
	path := "/api/admin/groups/" + group["id"].(string)
	entry := activityEntry(t, logs, spans, "admin.group.create", "success", created.Meta.RequestID)
	if fmt.Sprint(entry["group_id"]) != group["id"] || entry["actor_id"] != "4" {
		t.Fatalf("group/actor hasil create salah: %v", entry)
	}
	for _, tc := range []struct {
		method, suffix, body, event, status string
		httpStatus                          int
	}{
		{"POST", "/members", `{"user_id":"3"}`, "admin.group.member.add", "success", 201},
		{"PATCH", "", `{"leader_user_id":"3"}`, "admin.group.leader.set", "success", 200},
		// Perubahan label harus ikut rollback jika validasi leader gagal dalam transaction yang sama.
		{"PATCH", "", `{"name":"private-rollback-label","leader_user_id":"2"}`, "admin.group.leader.set", "error", 400},
		{"PATCH", "", `{"description":"private-new-description"}`, "admin.group.update", "success", 200},
		{"PUT", "/access/1", `{"access_level":"edit"}`, "admin.group.access.save", "success", 200},
		{"PUT", "/access/1", `{"access_level":"private-invalid-level"}`, "admin.group.access.save", "error", 400},
		{"DELETE", "/access/1", "", "admin.group.access.revoke", "success", 200},
		{"DELETE", "/members/3", "", "admin.group.member.remove", "success", 200},
		{"DELETE", "", "", "admin.group.archive", "success", 200},
	} {
		w := f.send(adminSession, tc.method, path+tc.suffix, tc.body)
		if w.Code != tc.httpStatus {
			t.Fatalf("%s %s: %d %s", tc.method, tc.suffix, w.Code, w.Body.String())
		}
		entry := activityEntry(t, logs, spans, tc.event, tc.status, w.Header().Get(telemetry.RequestIDHeader))
		if fmt.Sprint(entry["group_id"]) != group["id"] || entry["actor_id"] != "4" {
			t.Fatalf("group/actor salah: %v", entry)
		}
	}
	var name string
	if err := f.Platform.QueryRow(`SELECT name FROM groups WHERE id=$1`, group["id"]).Scan(&name); err != nil || name != "private-group-name" {
		t.Fatalf("perubahan yang ditolak harus rollback: %q %v", name, err)
	}
	traceText := ""
	for _, span := range spans.Ended() {
		traceText += fmt.Sprint(span.Attributes(), span.Events(), span.Status())
	}
	if strings.Contains(logs.String()+traceText, "private-") {
		t.Fatal("nama/deskripsi/input akses bocor ke telemetry")
	}
}
