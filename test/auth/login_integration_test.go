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
	"golang.org/x/crypto/bcrypt"
)

var testSessionConfig = SessionConfig{
	IdleTimeout:      30 * time.Minute,
	AbsoluteTimeout:  8 * time.Hour,
	RotationInterval: 15 * time.Minute,
	GracePeriod:      30 * time.Second,
}

// bikin router lengkap: middleware session, route auth, dan satu halaman terlindungi
func newAuthRouter(db *sql.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.LoadHTMLFiles("../../templates/login.html")

	RegisterRoutes(r, db, testSessionConfig)
	r.GET("/databases", RequireLogin(), func(c *gin.Context) {
		c.String(http.StatusOK, "daftar database")
	})
	// pura-pura halaman yang punya form, isinya cuma token CSRF
	r.GET("/form", RequireLogin(), func(c *gin.Context) {
		c.String(http.StatusOK, CSRFToken(c))
	})
	return r
}

// ambil token CSRF dari "halaman form" pakai cookie session yang dikasih
func csrfTokenFor(t *testing.T, r *gin.Engine, sessionCookie *http.Cookie) string {
	t.Helper()
	w := send(r, http.MethodGet, "/form", nil, sessionCookie)
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("gagal ambil token CSRF: status %d", w.Code)
	}
	return w.Body.String()
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

// login lewat form dan balikin cookie session-nya
func loginAs(t *testing.T, r *gin.Engine, username, password string) *http.Cookie {
	t.Helper()
	w := send(r, http.MethodPost, "/login", url.Values{"username": {username}, "password": {password}}, nil)
	cookie := sessionCookieFrom(w)
	if w.Code != http.StatusSeeOther || cookie == nil {
		t.Fatalf("login %s gagal: status %d", username, w.Code)
	}
	return cookie
}

// login benar dapat cookie aman, token di database cuma hash, logout nyabut session
func TestLoginAndLogout(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")

	// username ditulis beda huruf dan ada spasi, tetap harus bisa login
	w := send(r, http.MethodPost, "/login", url.Values{"username": {"  Editor "}, "password": {"rahasia123"}}, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/databases" {
		t.Fatalf("login benar: dapat %d ke %q", w.Code, w.Header().Get("Location"))
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

	if w := send(r, http.MethodGet, "/databases", nil, cookie); w.Code != http.StatusOK {
		t.Errorf("halaman terlindungi dengan cookie: dapat %d, harusnya 200", w.Code)
	}

	token := csrfTokenFor(t, r, cookie)
	w = send(r, http.MethodPost, "/logout", url.Values{"csrf_token": {token}}, cookie)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("logout: dapat %d ke %q", w.Code, w.Header().Get("Location"))
	}

	var revoked bool
	db.QueryRow(`SELECT revoked_at IS NOT NULL FROM sessions`).Scan(&revoked)
	if !revoked {
		t.Error("logout harus ngisi revoked_at")
	}

	// cookie lama dipakai lagi setelah logout harus ditolak
	if w := send(r, http.MethodGet, "/databases", nil, cookie); w.Code != http.StatusSeeOther {
		t.Errorf("cookie setelah logout: dapat %d, harusnya 303 ke /login", w.Code)
	}
}

// password salah, username ga ada, dan akun nonaktif semua dapat pesan yang sama
func TestLoginFailuresUseGenericMessage(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	nonaktif := insertUserWithPassword(t, db, "pensiun", "rahasia123")
	mustExec(t, db, `UPDATE users SET is_active = false WHERE id = $1`, nonaktif)

	attempts := []url.Values{
		{"username": {"editor"}, "password": {"tebakan-asal-99"}},
		{"username": {"ga-ada"}, "password": {"rahasia123"}},
		{"username": {"pensiun"}, "password": {"rahasia123"}},
	}
	for _, form := range attempts {
		w := send(r, http.MethodPost, "/login", form, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: dapat %d, harusnya 401", form.Get("username"), w.Code)
		}
		if !strings.Contains(w.Body.String(), "username atau password salah") {
			t.Errorf("%s: pesan error generik ga muncul", form.Get("username"))
		}
		if strings.Contains(w.Body.String(), form.Get("password")) {
			t.Errorf("%s: password ga boleh muncul di response", form.Get("username"))
		}
		if sessionCookieFrom(w) != nil {
			t.Errorf("%s: login gagal ga boleh ngasih cookie", form.Get("username"))
		}
	}
}

// login lagi dari browser yang sama harus dapat token baru dan nyabut session lama
func TestLoginAgainRevokesOldSession(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")

	oldCookie := loginAs(t, r, "editor", "rahasia123")
	w := send(r, http.MethodPost, "/login", url.Values{"username": {"editor"}, "password": {"rahasia123"}}, oldCookie)
	newCookie := sessionCookieFrom(w)
	if newCookie == nil || newCookie.Value == oldCookie.Value {
		t.Fatal("login ulang harus ngasih token baru")
	}

	if w := send(r, http.MethodGet, "/databases", nil, oldCookie); w.Code != http.StatusSeeOther {
		t.Errorf("token lama: dapat %d, harusnya ditolak", w.Code)
	}
	if w := send(r, http.MethodGet, "/databases", nil, newCookie); w.Code != http.StatusOK {
		t.Errorf("token baru: dapat %d, harusnya 200", w.Code)
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
		w := send(r, http.MethodGet, "/databases", nil, cookie)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("%s: dapat %d, harusnya 303 ke /login", tc.name, w.Code)
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
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("rahasia123")) != nil {
		t.Error("password admin harus disimpan sebagai hash bcrypt")
	}
}
