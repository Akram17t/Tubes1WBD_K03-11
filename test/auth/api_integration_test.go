package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	. "tubes-wbd/src/auth"
)

// opsi tambahan buat request JSON ke API
type apiOpts struct {
	cookie      *http.Cookie
	csrf        string
	origin      string
	contentType string
}

// kirim request JSON ke router, default Origin sama dengan host test (example.com)
func sendJSON(r http.Handler, method, path, body string, opts apiOpts) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if opts.contentType == "" {
		opts.contentType = "application/json"
	}
	req.Header.Set("Content-Type", opts.contentType)
	if opts.origin == "" {
		opts.origin = "http://example.com"
	}
	if opts.origin != "-" {
		req.Header.Set("Origin", opts.origin)
	}
	if opts.cookie != nil {
		req.AddCookie(opts.cookie)
	}
	if opts.csrf != "" {
		req.Header.Set(CSRFHeader, opts.csrf)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decode body JSON response ke map, test gagal kalau bukan JSON
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("response bukan JSON (status %d, type %q): %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body JSON rusak: %v", err)
	}
	return body
}

// ambil error.code dari response error, "" kalau ga ada
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	errBody, _ := decodeBody(t, w)["error"].(map[string]any)
	code, _ := errBody["code"].(string)
	return code
}

// login lewat API, balikin cookie session dan token CSRF-nya
func apiLoginAs(t *testing.T, r http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	w := sendJSON(r, http.MethodPost, LoginPath, `{"username":"`+username+`","password":"`+password+`"}`, apiOpts{})
	cookie := sessionCookieFrom(w)
	if w.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login API %s gagal: status %d %s", username, w.Code, w.Body.String())
	}
	data := decodeBody(t, w)["data"].(map[string]any)
	return cookie, data["csrf_token"].(string)
}

// login sukses dapat cookie + data user + token CSRF, me jalan, logout nyabut session dan 401 sesudahnya
func TestAPILoginMeLogout(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	userID := insertUserWithPassword(t, db, "editor", "rahasia123")

	w := sendJSON(r, http.MethodPost, LoginPath, `{"username":" Editor ","password":"rahasia123"}`, apiOpts{})
	cookie := sessionCookieFrom(w)
	if w.Code != http.StatusOK || cookie == nil || !cookie.HttpOnly {
		t.Fatalf("login: status %d, cookie %+v", w.Code, cookie)
	}
	data := decodeBody(t, w)["data"].(map[string]any)
	if data["id"] != strconv.FormatInt(userID, 10) || data["username"] != "editor" || data["is_platform_admin"] != false || data["csrf_token"] == "" {
		t.Errorf("data user salah: %v", data)
	}
	for _, leaked := range []string{"password", "hash", cookie.Value} {
		if strings.Contains(w.Body.String(), leaked) {
			t.Errorf("response login ga boleh berisi %q", leaked)
		}
	}

	w = sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{cookie: cookie})
	me := decodeBody(t, w)["data"].(map[string]any)
	if w.Code != http.StatusOK || me["username"] != "editor" || me["csrf_token"] == "" {
		t.Errorf("me: status %d data %v", w.Code, me)
	}

	w = sendJSON(r, http.MethodPost, "/api/auth/logout", "", apiOpts{cookie: cookie, csrf: me["csrf_token"].(string)})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":null`) {
		t.Fatalf("logout: status %d %s", w.Code, w.Body.String())
	}
	if cleared := sessionCookieFrom(w); cleared == nil || cleared.MaxAge >= 0 {
		t.Error("logout harus menghapus cookie session")
	}
	var revoked bool
	db.QueryRow(`SELECT revoked_at IS NOT NULL FROM sessions WHERE user_id = $1`, userID).Scan(&revoked)
	if !revoked {
		t.Error("logout harus mencabut session di database")
	}
	if w := sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{cookie: cookie}); w.Code != http.StatusUnauthorized {
		t.Errorf("me setelah logout: dapat %d, harusnya 401", w.Code)
	}
}

// password salah, user ga ada, dan user nonaktif dapat 401 dengan pesan yang sama tanpa cookie
func TestAPILoginInvalidCredentials(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	insertUserWithPassword(t, db, "mantan", "rahasia123")
	mustExec(t, db, `UPDATE users SET is_active = false WHERE username = 'mantan'`)

	var messages []string
	for _, body := range []string{
		`{"username":"editor","password":"salah-banget"}`,
		`{"username":"siapa","password":"rahasia123"}`,
		`{"username":"mantan","password":"rahasia123"}`,
	} {
		w := sendJSON(r, http.MethodPost, LoginPath, body, apiOpts{})
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != "unauthenticated" || sessionCookieFrom(w) != nil {
			t.Errorf("%s: status %d, harusnya 401 tanpa cookie", body, w.Code)
		}
		messages = append(messages, decodeBody(t, w)["error"].(map[string]any)["message"].(string))
	}
	if messages[0] != messages[1] || messages[1] != messages[2] {
		t.Errorf("pesan gagal login harus sama semua: %v", messages)
	}
}

// body bukan JSON, Content-Type salah, atau field asing ditolak 400
func TestAPILoginRejectsBadBody(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)

	cases := []struct {
		name, body, contentType string
	}{
		{"form biasa", "username=editor&password=rahasia123", "application/x-www-form-urlencoded"},
		{"JSON rusak", `{"username":`, ""},
		{"field asing", `{"username":"editor","password":"x","admin":true}`, ""},
		{"dua objek", `{"username":"a","password":"b"}{}`, ""},
		{"tipe salah", `{"username":123,"password":"b"}`, ""},
	}
	for _, tc := range cases {
		w := sendJSON(r, http.MethodPost, LoginPath, tc.body, apiOpts{contentType: tc.contentType})
		if w.Code != http.StatusBadRequest || errorCode(t, w) != "invalid_input" {
			t.Errorf("%s: dapat %d, harusnya 400 invalid_input", tc.name, w.Code)
		}
	}
}

// login API dari origin lain atau tanpa Origin/Referer ditolak 403 csrf_invalid
func TestAPILoginChecksOrigin(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	body := `{"username":"editor","password":"rahasia123"}`

	for _, origin := range []string{"http://evil.example", "-"} {
		w := sendJSON(r, http.MethodPost, LoginPath, body, apiOpts{origin: origin})
		if w.Code != http.StatusForbidden || errorCode(t, w) != "csrf_invalid" || sessionCookieFrom(w) != nil {
			t.Errorf("origin %q: dapat %d, harusnya 403 csrf_invalid", origin, w.Code)
		}
	}
}

// mutasi API tanpa header CSRF, token ngasal, atau token session lain ditolak; token benar diterima
func TestAPICSRFHeader(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	insertUserWithPassword(t, db, "viewer", "rahasia123")
	cookie, token := apiLoginAs(t, r, "editor", "rahasia123")
	_, otherToken := apiLoginAs(t, r, "viewer", "rahasia123")

	for name, bad := range map[string]string{"tanpa header": "", "token ngasal": "abc", "token session lain": otherToken} {
		w := sendJSON(r, http.MethodPost, "/api/auth/logout", "", apiOpts{cookie: cookie, csrf: bad})
		if w.Code != http.StatusForbidden || errorCode(t, w) != "csrf_invalid" {
			t.Errorf("%s: dapat %d, harusnya 403 csrf_invalid", name, w.Code)
		}
	}
	if w := sendJSON(r, http.MethodPost, "/api/auth/logout", "", apiOpts{cookie: cookie, csrf: token}); w.Code != http.StatusOK {
		t.Errorf("token benar: dapat %d, harusnya 200", w.Code)
	}
}

// API tanpa login dapat JSON 401, bukan redirect HTML; halaman lama tetap redirect
func TestAPIUnauthenticatedIsJSON(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)

	w := sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{})
	if w.Code != http.StatusUnauthorized || w.Header().Get("Location") != "" || errorCode(t, w) != "unauthenticated" {
		t.Errorf("me tanpa login: status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	if w := sendJSON(r, http.MethodPost, "/api/auth/logout", "", apiOpts{}); w.Code != http.StatusUnauthorized {
		t.Errorf("logout tanpa login: dapat %d, harusnya 401", w.Code)
	}
	if w := send(r, http.MethodGet, "/databases", nil, nil); w.Code != http.StatusSeeOther {
		t.Errorf("halaman lama tanpa login harus tetap redirect, dapat %d", w.Code)
	}
}

// rotasi tetap jalan lewat API dan token CSRF lama tetap valid karena secret session ga berubah
func TestAPIRotationKeepsCSRFToken(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	cookie, token := apiLoginAs(t, r, "editor", "rahasia123")

	makeRotationDue(t, db)
	w := sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{cookie: cookie})
	rotated := sessionCookieFrom(w)
	if w.Code != http.StatusOK || rotated == nil || rotated.Value == cookie.Value {
		t.Fatalf("rotasi lewat API: status %d, cookie baru %v", w.Code, rotated)
	}
	if w := sendJSON(r, http.MethodPost, "/api/auth/logout", "", apiOpts{cookie: rotated, csrf: token}); w.Code != http.StatusOK {
		t.Errorf("token CSRF setelah rotasi: dapat %d, harusnya 200", w.Code)
	}
}

// session yang kedaluwarsa ditolak 401 di API, login ulang nyabut session lama
func TestAPIExpiryAndRelogin(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")

	cookie, _ := apiLoginAs(t, r, "editor", "rahasia123")
	mustExec(t, db, `UPDATE sessions SET last_activity_at = now() - interval '31 minutes'`)
	if w := sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{cookie: cookie}); w.Code != http.StatusUnauthorized {
		t.Errorf("idle timeout: dapat %d, harusnya 401", w.Code)
	}

	oldCookie, _ := apiLoginAs(t, r, "editor", "rahasia123")
	w := sendJSON(r, http.MethodPost, LoginPath, `{"username":"editor","password":"rahasia123"}`, apiOpts{cookie: oldCookie})
	newCookie := sessionCookieFrom(w)
	if w.Code != http.StatusOK || newCookie == nil || newCookie.Value == oldCookie.Value {
		t.Fatalf("login ulang: status %d", w.Code)
	}
	if w := sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{cookie: oldCookie}); w.Code != http.StatusUnauthorized {
		t.Errorf("cookie lama setelah login ulang: dapat %d, harusnya 401", w.Code)
	}
}
