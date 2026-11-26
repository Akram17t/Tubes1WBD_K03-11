package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	. "tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

var testSessionConfig = SessionConfig{
	IdleTimeout:      30 * time.Minute,
	AbsoluteTimeout:  8 * time.Hour,
	RotationInterval: 15 * time.Minute,
	GracePeriod:      30 * time.Second,
}

// route terlindungi buat ngetes session; prefix /api biar balasan gagalnya JSON 401
const protectedPath = "/api/protected"

// bikin router lengkap: middleware session, route auth, dan satu route terlindungi
func newAuthRouter(db *sql.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, db, testSessionConfig)
	r.GET(protectedPath, RequireLogin(), func(c *gin.Context) {
		c.String(http.StatusOK, "terlindungi")
	})
	return r
}

// ambil token CSRF dari /api/auth/me pakai cookie session yang dikasih, sama kayak SPA
func csrfTokenFor(t *testing.T, r *gin.Engine, sessionCookie *http.Cookie) string {
	t.Helper()
	w := sendJSON(r, http.MethodGet, "/api/auth/me", "", apiOpts{cookie: sessionCookie})
	token, _ := decodeBody(t, w)["data"].(map[string]any)["csrf_token"].(string)
	if w.Code != http.StatusOK || token == "" {
		t.Fatalf("gagal ambil token CSRF: status %d", w.Code)
	}
	return token
}

// bikin user dengan password asli (di-hash), balikin id-nya
func insertUserWithPassword(t *testing.T, db *sql.DB, username, password string) int64 {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = db.QueryRow(`
		INSERT INTO users (username, password_hash, display_name)
		VALUES ($1, $2, $1) RETURNING id
	`, username, hash).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// kirim request ke router seolah dari halaman kita sendiri, cookie session boleh kosong
func send(r *gin.Engine, method, path string, form url.Values, sessionCookie *http.Cookie) *httptest.ResponseRecorder {
	var body *bytes.Buffer = &bytes.Buffer{}
	if form != nil {
		body = bytes.NewBufferString(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// httptest pakai host example.com, jadi Origin-nya disamain
	req.Header.Set("Origin", "http://example.com")
	if sessionCookie != nil {
		req.AddCookie(sessionCookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ambil cookie session dari response, nil kalau ga ada
func sessionCookieFrom(w *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" {
			return cookie
		}
	}
	return nil
}

// login lewat API dan balikin cookie session-nya
func loginAs(t *testing.T, r *gin.Engine, username, password string) *http.Cookie {
	t.Helper()
	cookie, _ := apiLoginAs(t, r, username, password)
	return cookie
}

// login benar dapat cookie aman, token di database cuma hash, logout nyabut session
func TestLoginAndLogout(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")

	// username ditulis beda huruf dan ada spasi, tetap harus bisa login
	w := sendJSON(r, http.MethodPost, LoginPath, `{"username":"  Editor ","password":"rahasia123"}`, apiOpts{})
	if w.Code != http.StatusOK {
		t.Fatalf("login benar: dapat %d", w.Code)
	}
	cookie := sessionCookieFrom(w)
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie session harus ada, HttpOnly, dan SameSite=Lax: %+v", cookie)
	}

	var storedHash []byte
	db.QueryRow(`SELECT token_hash FROM session_tokens`).Scan(&storedHash)
	expectedHash := sha256.Sum256([]byte(cookie.Value))
	if !bytes.Equal(storedHash, expectedHash[:]) || bytes.Equal(storedHash, []byte(cookie.Value)) {
		t.Error("database harus nyimpen hash token, bukan token aslinya")
	}

	if w := send(r, http.MethodGet, protectedPath, nil, cookie); w.Code != http.StatusOK {
		t.Errorf("route terlindungi dengan cookie: dapat %d, harusnya 200", w.Code)
	}

	token := csrfTokenFor(t, r, cookie)
	if w := sendJSON(r, http.MethodPost, "/api/auth/logout", "", apiOpts{cookie: cookie, csrf: token}); w.Code != http.StatusOK {
		t.Errorf("logout: dapat %d, harusnya 200", w.Code)
	}

	var revoked bool
	db.QueryRow(`SELECT revoked_at IS NOT NULL FROM sessions`).Scan(&revoked)
	if !revoked {
		t.Error("logout harus ngisi revoked_at")
	}

	// cookie lama dipakai lagi setelah logout harus ditolak
	if w := send(r, http.MethodGet, protectedPath, nil, cookie); w.Code != http.StatusUnauthorized {
		t.Errorf("cookie setelah logout: dapat %d, harusnya 401", w.Code)
	}
}

// session yang lewat idle timeout, absolute expiration, atau user-nya dinonaktifkan ditolak
func TestSessionExpiry(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	userID := insertUserWithPassword(t, db, "editor", "rahasia123")

	cases := []struct {
		name  string
		setup string
	}{
		{"idle timeout", `UPDATE sessions SET last_activity_at = now() - interval '31 minutes'`},
		{"absolute expiration", `UPDATE sessions SET created_at = now() - interval '9 hours',
			absolute_expires_at = now() - interval '1 second'`},
		{"user nonaktif", `UPDATE users SET is_active = false`},
	}
	for _, tc := range cases {
		mustExec(t, db, `UPDATE users SET is_active = true WHERE id = $1`, userID)
		mustExec(t, db, `TRUNCATE sessions CASCADE`)
		cookie := loginAs(t, r, "editor", "rahasia123")

		mustExec(t, db, tc.setup)
		if w := send(r, http.MethodGet, protectedPath, nil, cookie); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: dapat %d, harusnya 401", tc.name, w.Code)
		}
	}
}

// admin pertama dibuat sekali aja dari env, password-nya ke-hash
func TestEnsureAdmin(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := EnsureAdmin(ctx, db, "", ""); err == nil {
		t.Error("tanpa admin dan tanpa env harusnya error")
	}

	if err := EnsureAdmin(ctx, db, " Admin ", "rahasia123"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAdmin(ctx, db, "admin2", "lain"); err != nil {
		t.Fatal(err)
	}

	var count int
	var username, hash string
	db.QueryRow(`SELECT count(*) FROM users`).Scan(&count)
	db.QueryRow(`SELECT username, password_hash FROM users WHERE is_platform_admin`).Scan(&username, &hash)
	if count != 1 || username != "admin" {
		t.Errorf("harusnya 1 admin bernama admin, dapat %d user, username %q", count, username)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || !CheckPassword(hash, "rahasia123") {
		t.Error("password admin harus disimpan sebagai hash Argon2id")
	}
}
