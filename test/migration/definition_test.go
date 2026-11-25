package migration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/migration"
	"tubes-wbd/src/telemetry"
	testdb "tubes-wbd/test/database"

	"github.com/gin-gonic/gin"
)

type apiSession struct {
	cookie *http.Cookie
	csrf   string
}

type apiReply struct {
	status int
	Data   json.RawMessage `json:"data"`
	Meta   struct {
		RequestID string `json:"request_id"`
		HasMore   *bool  `json:"has_more"`
	} `json:"meta"`
	Error struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

type definitionFixture struct {
	*testdb.Fixture
	router         *gin.Engine
	service        migration.DefinitionService
	workflow       migration.WorkflowService
	editor, viewer apiSession
}

func newDefinitionFixture(t *testing.T) *definitionFixture {
	t.Helper()
	f := &definitionFixture{Fixture: testdb.New(t)}
	f.service = migration.DefinitionService{DB: f.Platform, Targets: f.Provider}
	f.workflow = migration.WorkflowService{DB: f.Platform, Validate: f.service.Validate, FindJob: migration.FindRequestJob}
	f.router = f.Router(t)
	f.router.Use(telemetry.RequestIDMiddleware())
	if err := migration.RegisterDefinitionRoutes(f.router, f.service, auth.CSRF()); err != nil {
		t.Fatal(err)
	}
	if err := migration.RegisterWorkflowRoutes(f.router, f.workflow, auth.CSRF()); err != nil {
		t.Fatal(err)
	}
	f.editor, f.viewer = f.login(t, "editor"), f.login(t, "viewer")
	return f
}

func (f *definitionFixture) login(t *testing.T, username string) apiSession {
	t.Helper()
	w := f.send(apiSession{}, http.MethodPost, auth.LoginPath, `{"username":"`+username+`","password":"test-password"}`)
	var body struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" {
			return apiSession{cookie, body.Data.CSRF}
		}
	}
	t.Fatal("cookie session tidak ditemukan")
	return apiSession{}
}

func (f *definitionFixture) send(session apiSession, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	if session.cookie != nil {
		request.AddCookie(session.cookie)
	}
	if session.csrf != "" {
		request.Header.Set(auth.CSRFHeader, session.csrf)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, request)
	return w
}

// call memeriksa status dan mengembalikan envelope JSON.
func (f *definitionFixture) call(t *testing.T, session apiSession, method, path, body string, status int, code string) apiReply {
	t.Helper()
	w := f.send(session, method, path, body)
	reply := apiReply{status: w.Code}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("%s %s bukan JSON (%d): %s", method, path, w.Code, w.Body.String())
	}
	if w.Code != status || reply.Error.Code != code {
		t.Fatalf("%s %s: dapat %d %q, harusnya %d %q: %s", method, path, w.Code, reply.Error.Code, status, code, w.Body.String())
	}
	return reply
}

func object(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

const base = "/api/databases/1/migrations"

const normalBody = `{"title":"Tambah nickname","description":null,
	"sql_up":"ALTER TABLE public.players ADD COLUMN nickname text",
	"sql_down":"ALTER TABLE public.players DROP COLUMN nickname",
	"irreversible":false,"irreversible_reason":null,"mode":"normal","batch_config":null}`

func TestDefinitionAPICreateReadAndPermissions(t *testing.T) {
	f := newDefinitionFixture(t)

	f.call(t, apiSession{}, "GET", base, "", 401, "unauthenticated")
	f.call(t, apiSession{cookie: f.editor.cookie}, "POST", base, normalBody, 403, "csrf_invalid")
	f.call(t, f.viewer, "POST", base, normalBody, 403, "forbidden")
	f.call(t, f.editor, "POST", base, `{"title":"x","sql_content":"SELECT 1"}`, 400, "invalid_input")
	reply := f.call(t, f.editor, "POST", base,
		`{"title":"x","sql_up":"SELECT 1; COMMIT","sql_down":"SELECT 1","mode":"normal"}`, 400, "invalid_sql")
	if reply.Error.Details["field"] != "sql_up" {
		t.Fatalf("details: %v", reply.Error.Details)
	}

	reply = f.call(t, f.editor, "POST", base, normalBody, 201, "")
	created := object(t, reply.Data)
	if created["id"] != "1" || created["database_id"] != "1" || created["created_by"] != "1" ||
		created["revision"] != float64(1) || created["position"] != "never_applied" ||
		created["locked_at"] != nil || created["batch_config"] != nil || created["description"] != nil ||
		created["active_execution_id"] != nil || reply.Meta.RequestID == "" {
		t.Fatalf("bentuk response create: %s", reply.Data)
	}
	if fmt.Sprint(created["allowed_actions"]) != "[edit request_up]" {
		t.Fatalf("aksi editor: %v", created["allowed_actions"])
	}
	// SQL hanya di-parse; kolom belum boleh ada di target
	var exists bool
	if err := f.Target.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name='players' AND column_name='nickname')`).Scan(&exists); err != nil || exists {
		t.Fatalf("membuat definisi mengeksekusi SQL: %v %v", exists, err)
	}

	viewed := object(t, f.call(t, f.viewer, "GET", base+"/1", "", 200, "").Data)
	if actions, ok := viewed["allowed_actions"].([]any); !ok || len(actions) != 0 {
		t.Fatalf("viewer tidak punya aksi: %v", viewed["allowed_actions"])
	}
	// migration database 1 tidak terbaca lewat database lain
	f.call(t, f.editor, "GET", "/api/databases/2/migrations/1", "", 404, "not_found")
	f.call(t, f.viewer, "GET", "/api/databases/2/migrations", "", 403, "forbidden")

	f.call(t, f.editor, "POST", base, normalBody, 201, "")
	reply = f.call(t, f.viewer, "GET", base+"?limit=1", "", 200, "")
	var list []map[string]any
	if err := json.Unmarshal(reply.Data, &list); err != nil || len(list) != 1 || list[0]["id"] != "2" ||
		reply.Meta.HasMore == nil || !*reply.Meta.HasMore {
		t.Fatalf("pagination: %s %v", reply.Data, err)
	}
	if got := f.call(t, f.editor, "GET", "/api/databases/2/migrations", "", 200, ""); string(got.Data) != "[]" {
		t.Fatalf("list kosong harus []: %s", got.Data)
	}
}

func TestDefinitionAPIUpdateRevisionReviewAndLock(t *testing.T) {
	f := newDefinitionFixture(t)
	f.call(t, f.editor, "POST", base, normalBody, 201, "")

	f.call(t, f.editor, "PATCH", base+"/1", `{"title":"tanpa revision"}`, 400, "invalid_input")
	f.call(t, f.viewer, "PATCH", base+"/1", `{"revision":1,"title":"x"}`, 403, "forbidden")
	// field yang tidak dikirim tidak berubah, dan revisi naik
	updated := object(t, f.call(t, f.editor, "PATCH", base+"/1", `{"revision":1,"title":"Judul baru"}`, 200, "").Data)
	if updated["revision"] != float64(2) || updated["title"] != "Judul baru" ||
		updated["sql_down"] != "ALTER TABLE public.players DROP COLUMN nickname" {
		t.Fatalf("hasil patch: %v", updated)
	}
	f.call(t, f.editor, "PATCH", base+"/1", `{"revision":1,"title":"basi"}`, 409, "state_conflict")
	f.call(t, f.editor, "PATCH", base+"/1", `{"revision":2,"sql_down":null}`, 400, "invalid_input")
	updated = object(t, f.call(t, f.editor, "PATCH", base+"/1",
		`{"revision":2,"sql_down":null,"irreversible":true,"irreversible_reason":"data hilang"}`, 200, "").Data)
	if updated["sql_down"] != nil || updated["irreversible"] != true {
		t.Fatalf("patch ke irreversible: %v", updated)
	}

	// submit lewat workflow memakai validator asli; review aktif membekukan definisi
	requests := "/api/databases/1/requests"
	f.call(t, f.editor, "POST", requests, `{"migration_id":"1","revision":2,"operation":"up"}`, 409, "state_conflict")
	f.call(t, f.editor, "POST", requests, `{"migration_id":"1","revision":3,"operation":"up"}`, 201, "")
	f.call(t, f.editor, "POST", requests+"/1/submit", "", 200, "")
	f.call(t, f.editor, "PATCH", base+"/1", `{"revision":3,"title":"saat review"}`, 409, "state_conflict")
	if _, err := migration.UpdateMigration(context.Background(), f.Platform, f.Editor, 1, 1, "form M1", "", "SELECT 1"); !errors.Is(err, migration.ErrConflict) {
		t.Fatalf("jalur M1 tidak boleh mengubah definisi yang direview: %v", err)
	}
	viewed := object(t, f.call(t, f.editor, "GET", base+"/1", "", 200, "").Data)
	if fmt.Sprint(viewed["allowed_actions"]) != "[request_up]" {
		t.Fatalf("aksi saat review: %v", viewed["allowed_actions"])
	}
	// setelah ditarik dan belum locked, edit boleh lagi
	f.call(t, f.editor, "POST", requests+"/1/cancel", "", 200, "")
	f.call(t, f.editor, "PATCH", base+"/1", `{"revision":3,"title":"setelah ditarik"}`, 200, "")

	testdb.Exec(t, f.Platform, `UPDATE migrations SET locked_at=now() WHERE id=1`)
	f.call(t, f.editor, "PATCH", base+"/1", `{"revision":4,"title":"terkunci"}`, 409, "immutable_migration")
}

// operation memanggil validator seperti workflow: dalam transaction milik caller.
func (f *definitionFixture) operation(t *testing.T, input migration.RequestInput) (migration.DefinitionSnapshot, error) {
	t.Helper()
	tx, err := f.Platform.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return f.service.Validate(context.Background(), tx, 1, input)
}

func (f *definitionFixture) expect(t *testing.T, name, code string, input migration.RequestInput) {
	t.Helper()
	_, err := f.operation(t, input)
	if code == "" {
		if err != nil {
			t.Fatalf("%s: harusnya sah: %v", name, err)
		}
		return
	}
	definitionErr, ok := errors.AsType[*migration.DefinitionError](err)
	if !ok || definitionErr.Code != code {
		t.Fatalf("%s: harusnya %s, dapat %v", name, code, err)
	}
}

func request(migrationID int64, operation string, target, source int64) migration.RequestInput {
	input := migration.RequestInput{MigrationID: migrationID, Revision: 1, Operation: operation}
	if target != 0 {
		input.TargetExecutionID = &target
	}
	if source != 0 {
		input.SourceJobID = &source
	}
	return input
}

func TestOperationValidationFollowsLinearHistory(t *testing.T) {
	f := newDefinitionFixture(t)
	testdb.Exec(t, f.Platform, `
		INSERT INTO migrations (id,database_id,title,sql_content,sql_down,irreversible,irreversible_reason,created_by) VALUES
		(1,1,'A','CREATE TABLE a(id int)','DROP TABLE a',false,NULL,1),
		(2,1,'B','CREATE TABLE b(id int)','DROP TABLE b',false,NULL,1),
		(3,1,'C','CREATE TABLE c(id int)','DROP TABLE c',false,NULL,1),
		(4,1,'D','DROP TABLE x',NULL,true,'data hilang',1),
		(5,1,'Legacy tanpa down','SELECT 1',NULL,false,NULL,1)`)
	succeed := func(id, migrationID int64, operation string, target any, sequence int) {
		testdb.Exec(t, f.Platform, `INSERT INTO migration_executions
			(id,migration_id,executed_by,sql_snapshot,status,operation,target_execution_id,history_sequence,finished_at)
			VALUES ($1,$2,1,'x','success',$3,$4,$5,now())`, id, migrationID, operation, target, sequence)
	}

	snapshot, err := f.operation(t, request(1, "up", 0, 0))
	if err != nil || snapshot.SQLUp != "CREATE TABLE a(id int)" || snapshot.Mode != "normal" || snapshot.DatabaseID != 1 {
		t.Fatalf("snapshot dari storage: %+v %v", snapshot, err)
	}
	f.expect(t, "reversible tanpa sql down", "invalid_input", request(5, "up", 0, 0))
	f.expect(t, "rollback tanpa history", "state_conflict", request(1, "rollback", 99, 0))
	f.expect(t, "revisi basi", "state_conflict", migration.RequestInput{MigrationID: 1, Revision: 2, Operation: "up"})

	succeed(11, 1, "up", nil, 1)
	succeed(12, 2, "up", nil, 2)
	f.expect(t, "up ulang", "state_conflict", request(1, "up", 0, 0))
	f.expect(t, "rollback bukan terakhir", "state_conflict", request(1, "rollback", 11, 0))
	f.expect(t, "rollback terakhir", "", request(2, "rollback", 12, 0))
	f.expect(t, "target milik migration lain", "state_conflict", request(1, "rollback", 12, 0))

	succeed(13, 2, "rollback", 12, 3)
	succeed(14, 1, "rollback", 11, 4)
	f.expect(t, "forward melompati urutan", "state_conflict", request(2, "forward", 12, 0))
	f.expect(t, "forward berurutan", "", request(1, "forward", 11, 0))
	listed := object(t, f.call(t, f.editor, "GET", base+"/1", "", 200, "").Data)
	if listed["position"] != "rolled_back" || listed["forward_execution_id"] != "11" ||
		!strings.Contains(fmt.Sprint(listed["allowed_actions"]), "request_forward") {
		t.Fatalf("detail kandidat forward: %v", listed)
	}

	// up baru membuang cabang forward lama
	succeed(15, 3, "up", nil, 5)
	f.expect(t, "forward cabang usang", "state_conflict", request(1, "forward", 11, 0))
	f.expect(t, "up migration usang", "state_conflict", request(1, "up", 0, 0))

	succeed(16, 4, "up", nil, 6)
	f.expect(t, "rollback irreversible", "not_reversible", request(4, "rollback", 16, 0))

	testdb.Exec(t, f.Platform, `UPDATE database_connections SET is_active=false WHERE id=1`)
	f.expect(t, "koneksi nonaktif", "state_conflict", request(3, "rollback", 15, 0))
}

func TestOperationValidationBlockersAndCompensation(t *testing.T) {
	f := newDefinitionFixture(t)
	testdb.Exec(t, f.Platform, `
		INSERT INTO migrations (id,database_id,title,sql_content,sql_down,created_by) VALUES
		(1,1,'Sumber','SELECT 1','SELECT 2',1),(2,1,'Lain','SELECT 3','SELECT 4',1);
		INSERT INTO migration_requests (id,database_id,migration_id,revision,operation,requested_by,definition_snapshot)
		VALUES (1,1,1,1,'up',1,'{}'),(2,1,2,1,'up',1,'{}');
		INSERT INTO migration_jobs (id,request_id,database_id,migration_id,revision,operation,execution_actor_id,status)
		VALUES (1,1,1,1,1,'up',1,'partial')`)

	f.expect(t, "migration lain saat partial", "database_blocked", request(2, "up", 0, 0))
	f.expect(t, "kompensasi job penahan", "", request(1, "compensate", 0, 1))
	_, err := f.operation(t, request(2, "up", 0, 0))
	blocked, _ := errors.AsType[*migration.DefinitionError](err)
	if blocked.Details["blocking_job_id"] != "1" || blocked.Details["block_reason"] != "partial" {
		t.Fatalf("detail blocker: %v", blocked.Details)
	}
	listed := object(t, f.call(t, f.editor, "GET", base+"/2", "", 200, "").Data)
	if fmt.Sprint(listed["allowed_actions"]) != "[edit]" {
		t.Fatalf("aksi saat database diblokir: %v", listed["allowed_actions"])
	}

	// job yang antre retry setelah batch committed tetap menahan database
	testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET status='queued', committed_batches=2 WHERE id=1`)
	f.expect(t, "retry dengan efek committed", "database_blocked", request(2, "up", 0, 0))
	for _, status := range []string{"partially_reverted", "outcome_unknown"} {
		testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET status=$1 WHERE id=1`, status)
		f.expect(t, status, "database_blocked", request(2, "up", 0, 0))
	}
	testdb.Exec(t, f.Platform, `UPDATE migration_jobs SET status='failed', committed_batches=0 WHERE id=1`)
	f.expect(t, "failed tanpa efek tidak menahan", "", request(2, "up", 0, 0))
	f.expect(t, "kompensasi job bukan partial", "state_conflict", request(1, "compensate", 0, 1))
	if _, err := f.operation(t, request(1, "compensate", 0, 99)); !errors.Is(err, migration.ErrNotFound) {
		t.Fatalf("source job tidak ada: %v", err)
	}

	testdb.Exec(t, f.Platform, `
		INSERT INTO migration_requests (id,database_id,migration_id,revision,operation,source_job_id,requested_by,definition_snapshot)
		VALUES (3,1,1,1,'compensate',1,1,'{}');
		INSERT INTO migration_jobs (id,request_id,database_id,migration_id,revision,operation,execution_actor_id)
		VALUES (3,3,1,1,1,'compensate',1);
		UPDATE migration_jobs SET status='partial', compensation_job_id=3 WHERE id=1`)
	_, err = f.operation(t, request(1, "compensate", 0, 1))
	existing, ok := errors.AsType[*migration.DefinitionError](err)
	if !ok || existing.Code != "state_conflict" || existing.Details["existing_job_id"] != "3" {
		t.Fatalf("kompensasi kedua: %v", err)
	}
}

func TestBatchDefinitionChecksTargetCatalog(t *testing.T) {
	f := newDefinitionFixture(t)
	testdb.Exec(t, f.Target, `
		CREATE TABLE public.batch_demo (id bigint PRIMARY KEY, value int);
		CREATE TABLE public.int_key (id int PRIMARY KEY, value int);
		CREATE TABLE public.composite_key (id bigint, other bigint, value int, PRIMARY KEY (id, other));
		CREATE TABLE public.no_key (id bigint, value int);
		CREATE VIEW public.batch_view AS SELECT * FROM public.batch_demo`)
	create := func(table, key string) int64 {
		query := fmt.Sprintf("UPDATE public.%s SET value = value + 1 WHERE %s = ANY($1::bigint[])", table, key)
		down := strings.Replace(query, "+ 1", "- 1", 1)
		definition, err := f.service.Create(context.Background(), f.Editor, 1, migration.DefinitionInput{
			Title: table, SQLUp: query, SQLDown: &down, Mode: "batch",
			BatchConfig: &migration.BatchConfig{Schema: "public", Table: table, KeyColumn: key},
		})
		if err != nil {
			t.Fatalf("draft batch %s: %v", table, err)
		}
		return definition.ID
	}
	valid := create("batch_demo", "id")
	snapshot, err := f.operation(t, request(valid, "up", 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	config := object(t, snapshot.BatchConfig)
	if config["batch_size"] != float64(1000) || config["key_column"] != "id" {
		t.Fatalf("batch_config tersimpan: %v", config)
	}
	for _, test := range []struct{ table, key string }{
		{"missing_table", "id"}, {"batch_demo", "missing_column"}, {"int_key", "id"},
		{"composite_key", "id"}, {"no_key", "id"}, {"batch_view", "id"},
	} {
		f.expect(t, test.table+"."+test.key, "invalid_input", request(create(test.table, test.key), "up", 0, 0))
	}
	f.service.Targets = nil
	f.expect(t, "target tidak tersedia", "target_unavailable", request(valid, "up", 0, 0))
}
