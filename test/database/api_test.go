package database_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tubes-wbd/src/database"
	"tubes-wbd/src/telemetry"
	testdb "tubes-wbd/test/database"

	"github.com/gin-gonic/gin"
)

func apiRequest(r *gin.Engine, method, path, body, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", token)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestConnectionAPI menguji akses, validasi, dan perubahan koneksi melalui handler JSON sesungguhnya.
func TestConnectionAPI(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	r := f.Router(t)
	r.Use(telemetry.RequestIDMiddleware())
	database.RegisterAdminRoutes(r, service)
	admin := testdb.Login(t, r, "admin")
	editor := testdb.Login(t, r, "editor")
	token := testdb.CSRF(t, r, admin)
	for _, tc := range []struct {
		method, path, body, csrf string
		cookie                   *http.Cookie
		status                   int
	}{
		{"GET", "/api/admin/connections", "", "", nil, 401},
		{"GET", "/api/admin/connections", "", "", editor, 403},
		{"PATCH", "/api/admin/connections/1", `{}`, "", admin, 403},
		{"PATCH", "/api/admin/connections/1", `{"host":null}`, token, admin, 400},
		{"PATCH", "/api/admin/connections/1", `{"unknown":true}`, token, admin, 400},
		{"PATCH", "/api/admin/connections/1", `{"name":"Updated","password":""}`, token, admin, 200},
		{"POST", "/api/admin/connections/1/test", "", token, admin, 200},
		{"PATCH", "/api/admin/connections/1", `{"name":"Invalid","port":0,"is_active":false}`, token, admin, 400},
	} {
		w := apiRequest(r, tc.method, tc.path, tc.body, tc.csrf, tc.cookie)
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "password") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	// Pesan validator harus sampai ke SPA pada create maupun PATCH, tanpa membocorkan input sensitif.
	for _, method := range []string{"POST", "PATCH"} {
		path := "/api/admin/connections"
		if method == "PATCH" {
			path += "/1"
		}
		body := `{"name":"Invalid host","host":"local host","database_name":"demo","db_username":"demo","password":"private-test-secret"}`
		w := apiRequest(r, method, path, body, token, admin)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "host tidak boleh mengandung spasi") || strings.Contains(w.Body.String(), "private-test-secret") {
			t.Fatalf("%s: pesan host hilang atau input bocor: %d %s", method, w.Code, w.Body.String())
		}
	}
	w := apiRequest(r, "GET", "/api/admin/connections/1", "", "", admin)
	var saved struct{ Data database.Connection }
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || w.Code != 200 || saved.Data.Name != "Updated" || !saved.Data.IsActive {
		t.Fatalf("PATCH gagal harus mempertahankan data sebelumnya: %s, %v", w.Body.String(), err)
	}
	body := `{"name":"New API","host":"localhost","database_name":"api_demo","db_username":"demo","password":"api-test-secret","ssl_mode":"disable"}`
	w = apiRequest(r, "POST", "/api/admin/connections", body, token, admin)
	if w.Code != 201 || strings.Contains(w.Body.String(), "api-test-secret") || !strings.Contains(w.Body.String(), `"id":"`) {
		t.Fatalf("create API: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(r, "GET", "/api/admin/connections?limit=1", "", "", admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"has_more":true`) || w.Header().Get("X-Request-ID") == "" {
		t.Fatalf("pagination koneksi: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(r, "PATCH", "/api/admin/connections/1", `{"is_active":false}`, token, admin)
	var sessions int
	err := f.Platform.QueryRow(`SELECT count(*) FROM sessions WHERE user_id=1 AND revoked_at IS NULL`).Scan(&sessions)
	if w.Code != 200 || err != nil || sessions != 0 {
		t.Fatalf("nonaktifkan koneksi harus mencabut session: %d, %d, %v", w.Code, sessions, err)
	}
}

func TestViewerAPI(t *testing.T) {
	f := testdb.New(t)
	testdb.Exec(t, f.Target, `CREATE TABLE public._dbplatform_batch_scope(id bigint)`)
	r := f.Router(t)
	r.Use(telemetry.RequestIDMiddleware())
	database.RegisterRoutes(r, f.Platform, f.Provider)
	viewer := testdb.Login(t, r, "viewer")
	outsider := testdb.Login(t, r, "outsider")
	for _, tc := range []struct {
		path   string
		cookie *http.Cookie
		status int
	}{
		{"/api/databases", nil, 401},
		{"/api/databases/1/data?table=players", outsider, 403},
		{"/api/databases?limit=0", viewer, 400},
		{"/api/databases", viewer, 200},
		{"/api/databases/1", viewer, 200},
		{"/api/databases/1/schemas", viewer, 200},
		{"/api/databases/1/tables?schema=public", viewer, 200},
		{"/api/databases/1/table?table=players", viewer, 200},
		{"/api/databases/1/data?table=players&offset=-1", viewer, 400},
		{"/api/databases/1/data?table=missing", viewer, 404},
		{"/api/databases/1/data?table=_dbplatform_batch_scope", viewer, 404},
	} {
		w := apiRequest(r, "GET", tc.path, "", "", tc.cookie)
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "_dbplatform_batch_scope") {
			t.Fatalf("GET %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		query   string
		rows    int
		hasMore bool
	}{{"offset=50", 50, true}, {"offset=100", 23, false}, {"limit=123", 123, false}} {
		w := apiRequest(r, "GET", "/api/databases/1/data?table=players&"+tc.query, "", "", viewer)
		var body struct{ Data database.TableData }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || len(body.Data.Rows) != tc.rows || body.Data.HasMore != tc.hasMore {
			t.Fatalf("pagination %s: %d %s, %v", tc.query, w.Code, w.Body.String(), err)
		}
	}
}
