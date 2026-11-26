package admin_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tubes-wbd/src/auth"
)

// sesi login API: cookie session + token CSRF buat header
type apiSession struct {
	cookie *http.Cookie
	csrf   string
}

// kirim request JSON ke API, sesi boleh kosong (belum login)
func callAPI(r http.Handler, method, path, body string, s apiSession) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	if s.cookie != nil {
		req.AddCookie(s.cookie)
	}
	if s.csrf != "" {
		req.Header.Set(auth.CSRFHeader, s.csrf)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var decoded map[string]any
	json.Unmarshal(w.Body.Bytes(), &decoded)
	return w, decoded
}

// login lewat API pakai password "rahasia123"
func apiLogin(t *testing.T, r http.Handler, username string) apiSession {
	t.Helper()
	w, body := callAPI(r, http.MethodPost, auth.LoginPath, `{"username":"`+username+`","password":"rahasia123"}`, apiSession{})
	if w.Code != http.StatusOK {
		t.Fatalf("login API %s: status %d %s", username, w.Code, w.Body.String())
	}
	s := apiSession{csrf: body["data"].(map[string]any)["csrf_token"].(string)}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			s.cookie = c
		}
	}
	return s
}

// ambil error.code dari body response
func codeOf(body map[string]any) string {
	errBody, _ := body["error"].(map[string]any)
	code, _ := errBody["code"].(string)
	return code
}

// ambil field data sebagai map (buat response satu objek)
func dataOf(body map[string]any) map[string]any {
	data, _ := body["data"].(map[string]any)
	return data
}

// belum login 401 JSON, user biasa 403 JSON
func TestUserAPINeedsAdmin(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "budi", false)

	if w, body := callAPI(r, http.MethodGet, "/api/admin/users", "", apiSession{}); w.Code != http.StatusUnauthorized || codeOf(body) != "unauthenticated" {
		t.Errorf("belum login: dapat %d %v", w.Code, body)
	}
	budi := apiLogin(t, r, "budi")
	if w, body := callAPI(r, http.MethodGet, "/api/admin/users", "", budi); w.Code != http.StatusForbidden || codeOf(body) != "forbidden" {
		t.Errorf("user biasa: dapat %d %v", w.Code, body)
	}
}

// bikin user: 201, password jadi hash, nama tampilan default, dobel 409, input jelek 400
func TestUserAPICreate(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	admin := apiLogin(t, r, "admin")

	w, body := callAPI(r, http.MethodPost, "/api/admin/users", `{"username":" Budi ","password":"password-budi"}`, admin)
	data := dataOf(body)
	if w.Code != http.StatusCreated || data["username"] != "budi" || data["display_name"] != "budi" || data["is_active"] != true {
		t.Fatalf("bikin user: status %d data %v", w.Code, data)
	}
	if _, isString := data["id"].(string); !isString || strings.Contains(w.Body.String(), "password") {
		t.Errorf("id harus string dan password ga boleh ikut di response: %s", w.Body.String())
	}
	var hash string
	db.QueryRow(`SELECT password_hash FROM users WHERE username = 'budi'`).Scan(&hash)
	if !auth.CheckPassword(hash, "password-budi") {
		t.Error("password harus disimpan sebagai hash yang cocok")
	}

	cases := []struct {
		name, body, code string
		status           int
	}{
		{"username dobel", `{"username":"BUDI","password":"password-lain"}`, "state_conflict", http.StatusConflict},
		{"username kosong", `{"username":" ","password":"password-lain"}`, "invalid_input", http.StatusBadRequest},
		{"password pendek", `{"username":"cici","password":"pendek"}`, "invalid_input", http.StatusBadRequest},
		{"field asing", `{"username":"cici","password":"password-ok","role":"root"}`, "invalid_input", http.StatusBadRequest},
	}
	for _, tc := range cases {
		if w, body := callAPI(r, http.MethodPost, "/api/admin/users", tc.body, admin); w.Code != tc.status || codeOf(body) != tc.code {
			t.Errorf("%s: dapat %d %s, harusnya %d %s", tc.name, w.Code, codeOf(body), tc.status, tc.code)
		}
	}
	if w, _ := callAPI(r, http.MethodPost, "/api/admin/users", `{"username":"dodi","password":"password-dodi"}`, apiSession{cookie: admin.cookie}); w.Code != http.StatusForbidden {
		t.Errorf("tanpa header CSRF: dapat %d, harusnya 403", w.Code)
	}
}

// list pakai pagination dan has_more, detail user, ID ngaco atau ga ada 404
func TestUserAPIListAndGet(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	insertUser(t, db, "cici", false)
	admin := apiLogin(t, r, "admin")

	w, body := callAPI(r, http.MethodGet, "/api/admin/users?limit=2", "", admin)
	items, _ := body["data"].([]any)
	meta := body["meta"].(map[string]any)
	if w.Code != http.StatusOK || len(items) != 2 || meta["has_more"] != true || meta["limit"] != float64(2) {
		t.Errorf("halaman 1: status %d, %d item, meta %v", w.Code, len(items), meta)
	}
	_, body = callAPI(r, http.MethodGet, "/api/admin/users?limit=2&offset=2", "", admin)
	items, _ = body["data"].([]any)
	if len(items) != 1 || body["meta"].(map[string]any)["has_more"] != false || items[0].(map[string]any)["username"] != "cici" {
		t.Errorf("halaman 2 harus cuma cici tanpa has_more: %v", body)
	}

	w, body = callAPI(r, http.MethodGet, "/api/admin/users/"+itoa(budiID), "", admin)
	if w.Code != http.StatusOK || dataOf(body)["username"] != "budi" {
		t.Errorf("detail budi: status %d %v", w.Code, body)
	}
	for _, id := range []string{"999999", "abc", "0"} {
		if w, body := callAPI(r, http.MethodGet, "/api/admin/users/"+id, "", admin); w.Code != http.StatusNotFound || codeOf(body) != "not_found" {
			t.Errorf("id %s: dapat %d, harusnya 404", id, w.Code)
		}
	}
}

// PATCH sebagian: field lain ga berubah, efek ke session sama kayak M1, proteksi akun sendiri
func TestUserAPIPatch(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	adminID := insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	insertUser(t, db, "cici", false)
	admin := apiLogin(t, r, "admin")
	path := "/api/admin/users/" + itoa(budiID)

	w, body := callAPI(r, http.MethodPatch, path, `{"display_name":"Budi Santoso"}`, admin)
	data := dataOf(body)
	if w.Code != http.StatusOK || data["display_name"] != "Budi Santoso" || data["username"] != "budi" || data["is_platform_admin"] != false {
		t.Errorf("ganti nama tampilan aja: status %d data %v", w.Code, data)
	}

	apiLogin(t, r, "budi")
	callAPI(r, http.MethodPatch, path, `{"is_platform_admin":true}`, admin)
	var version int
	db.QueryRow(`SELECT privilege_version FROM users WHERE id = $1`, budiID).Scan(&version)
	if version != 1 || activeSessions(t, db, budiID) != 1 {
		t.Errorf("jadiin admin harus naikin privilege tanpa nyabut session (version %d)", version)
	}

	steps := []struct{ name, body string }{
		{"ganti password", `{"password":"password-baru"}`},
		{"cabut admin", `{"is_platform_admin":false}`},
		{"nonaktifkan", `{"is_active":false}`},
	}
	for _, step := range steps {
		freshSession(t, r, db, budiID)
		if w, _ := callAPI(r, http.MethodPatch, path, step.body, admin); w.Code != http.StatusOK || activeSessions(t, db, budiID) != 0 {
			t.Errorf("%s: status %d, session harus dicabut", step.name, w.Code)
		}
	}
	if w, _ := callAPI(r, http.MethodPost, auth.LoginPath, `{"username":"budi","password":"rahasia123"}`, apiSession{}); w.Code != http.StatusUnauthorized {
		t.Error("akun nonaktif ga boleh bisa login")
	}
	if w, body := callAPI(r, http.MethodPatch, path, `{"is_active":true}`, admin); w.Code != http.StatusOK || dataOf(body)["is_active"] != true {
		t.Errorf("aktifkan lagi: status %d", w.Code)
	}

	cases := []struct {
		name, path, body, code string
		status                 int
	}{
		{"body kosong", path, `{}`, "invalid_input", http.StatusBadRequest},
		{"username dipakai", path, `{"username":"cici"}`, "state_conflict", http.StatusConflict},
		{"password pendek", path, `{"password":"x"}`, "invalid_input", http.StatusBadRequest},
		{"cabut admin sendiri", "/api/admin/users/" + itoa(adminID), `{"is_platform_admin":false}`, "forbidden", http.StatusForbidden},
		{"nonaktifkan diri sendiri", "/api/admin/users/" + itoa(adminID), `{"is_active":false}`, "forbidden", http.StatusForbidden},
		{"user ga ada", "/api/admin/users/999999", `{"display_name":"x"}`, "not_found", http.StatusNotFound},
	}
	for _, tc := range cases {
		if w, body := callAPI(r, http.MethodPatch, tc.path, tc.body, admin); w.Code != tc.status || codeOf(body) != tc.code {
			t.Errorf("%s: dapat %d %s, harusnya %d %s", tc.name, w.Code, codeOf(body), tc.status, tc.code)
		}
	}
	var stillAdmin, stillActive bool
	db.QueryRow(`SELECT is_platform_admin, is_active FROM users WHERE id = $1`, adminID).Scan(&stillAdmin, &stillActive)
	if !stillAdmin || !stillActive {
		t.Error("proteksi akun sendiri gagal, admin berubah")
	}
}

// balikin password budi ke "rahasia123" + aktifin, lalu login biar ada session aktif buat dicek
func freshSession(t *testing.T, r http.Handler, db *sql.DB, userID int64) {
	t.Helper()
	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE users SET password_hash = $2, is_active = true WHERE id = $1`, userID, hash)
	if apiLogin(t, r, "budi"); activeSessions(t, db, userID) == 0 {
		t.Fatal("gagal bikin session aktif buat budi")
	}
}
