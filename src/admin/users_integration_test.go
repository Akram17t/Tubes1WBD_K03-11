package admin

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// buka test-db dan kosongin tabel, skip kalau TEST_DATABASE_URL ga diisi
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL kosong, integration test dilewati")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`TRUNCATE users, groups, database_connections RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return db
}

// router lengkap kayak di aplikasi: auth dulu, baru route admin
func newAdminRouter(db *sql.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.LoadHTMLGlob("../../templates/*.html")
	auth.RegisterRoutes(r, db, auth.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		AbsoluteTimeout:  8 * time.Hour,
		RotationInterval: 15 * time.Minute,
		GracePeriod:      30 * time.Second,
	})
	RegisterRoutes(r, db)
	return r
}

// bikin user dengan password beneran, balikin id-nya
func insertUser(t *testing.T, db *sql.DB, username string, isAdmin bool) int64 {
	t.Helper()
	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = db.QueryRow(`
		INSERT INTO users (username, password_hash, display_name, is_platform_admin)
		VALUES ($1, $2, $1, $3) RETURNING id
	`, username, hash, isAdmin).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// kirim request seolah dari halaman kita sendiri, cookie boleh nil
func send(r *gin.Engine, method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	body := &bytes.Buffer{}
	if form != nil {
		body = bytes.NewBufferString(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Origin", "http://example.com")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// login pakai password "rahasia123", balikin cookie session (nil kalau gagal)
func login(r *gin.Engine, username string) *http.Cookie {
	w := send(r, http.MethodPost, "/login", url.Values{"username": {username}, "password": {"rahasia123"}}, nil)
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

// POST sebagai admin, token CSRF diambil dari halaman form kayak browser beneran
func adminPost(t *testing.T, r *gin.Engine, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	page := send(r, http.MethodGet, "/admin/users/new", nil, cookie)
	match := csrfPattern.FindStringSubmatch(page.Body.String())
	if match == nil {
		t.Fatalf("token CSRF ga ketemu di halaman form (status %d)", page.Code)
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf_token", match[1])
	return send(r, http.MethodPost, path, form, cookie)
}

// hitung session yang masih aktif punya user
func activeSessions(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var count int
	db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&count)
	return count
}

// belum login diarahin ke /login, user biasa ditolak 403
func TestAdminRoutesNeedAdmin(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "budi", false)

	w := send(r, http.MethodGet, "/admin/users", nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("belum login: dapat %d ke %q", w.Code, w.Header().Get("Location"))
	}

	cookie := login(r, "budi")
	if w := send(r, http.MethodGet, "/admin/users", nil, cookie); w.Code != http.StatusForbidden {
		t.Errorf("user biasa: dapat %d, harusnya 403", w.Code)
	}
}

// admin bikin user, password ke-hash, username dobel 409, input jelek 400
func TestAdminCreatesUser(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	admin := login(r, "admin")

	w := adminPost(t, r, admin, "/admin/users", url.Values{"username": {" Budi "}, "password": {"password-budi"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("bikin user: dapat %d, harusnya 303", w.Code)
	}

	var hash, displayName string
	db.QueryRow(`SELECT password_hash, display_name FROM users WHERE username = 'budi'`).Scan(&hash, &displayName)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("password-budi")) != nil {
		t.Error("password harus disimpan sebagai hash bcrypt")
	}
	if displayName != "budi" {
		t.Errorf("nama tampilan kosong harusnya diisi username, dapat %q", displayName)
	}

	if w := send(r, http.MethodGet, "/admin/users", nil, admin); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("budi")) {
		t.Errorf("daftar user: dapat %d atau budi ga muncul", w.Code)
	}

	tests := []struct {
		name string
		form url.Values
		want int
	}{
		{"username dobel", url.Values{"username": {"BUDI"}, "password": {"password-lain"}}, http.StatusConflict},
		{"username kosong", url.Values{"username": {"  "}, "password": {"password-lain"}}, http.StatusBadRequest},
		{"password kependekan", url.Values{"username": {"cici"}, "password": {"pendek"}}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		if w := adminPost(t, r, admin, "/admin/users", tt.form); w.Code != tt.want {
			t.Errorf("%s: dapat %d, harusnya %d", tt.name, w.Code, tt.want)
		}
	}
}

// dijadiin admin naikin privilege_version, ganti password dan cabut admin nyabut session
func TestAdminUpdateUser(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	adminID := insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	admin := login(r, "admin")
	login(r, "budi")
	path := "/admin/users/" + itoa(budiID) + "/update"

	w := adminPost(t, r, admin, path, url.Values{"username": {"budi"}, "is_platform_admin": {"on"}})
	var version int
	db.QueryRow(`SELECT privilege_version FROM users WHERE id = $1`, budiID).Scan(&version)
	if w.Code != http.StatusSeeOther || version != 1 || activeSessions(t, db, budiID) != 1 {
		t.Errorf("jadiin admin: status %d, privilege_version %d, harusnya naik tanpa nyabut session", w.Code, version)
	}

	adminPost(t, r, admin, path, url.Values{"username": {"budi"}, "is_platform_admin": {"on"}, "password": {"password-baru"}})
	if activeSessions(t, db, budiID) != 0 {
		t.Error("ganti password harus nyabut session")
	}

	login(r, "budi") // password lama udah ga berlaku
	if activeSessions(t, db, budiID) != 0 {
		t.Error("password lama harusnya ga bisa dipakai login")
	}
	w = send(r, http.MethodPost, "/login", url.Values{"username": {"budi"}, "password": {"password-baru"}}, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login password baru: dapat %d", w.Code)
	}

	adminPost(t, r, admin, path, url.Values{"username": {"budi"}})
	if activeSessions(t, db, budiID) != 0 {
		t.Error("cabut admin harus nyabut session")
	}

	insertUser(t, db, "cici", false)
	if w := adminPost(t, r, admin, path, url.Values{"username": {"cici"}}); w.Code != http.StatusConflict {
		t.Errorf("ganti ke username yang dipakai: dapat %d, harusnya 409", w.Code)
	}
	if w := adminPost(t, r, admin, "/admin/users/"+itoa(adminID)+"/update", url.Values{"username": {"admin"}}); w.Code != http.StatusBadRequest {
		t.Errorf("cabut admin sendiri: dapat %d, harusnya 400", w.Code)
	}
	if w := adminPost(t, r, admin, "/admin/users/999/update", url.Values{"username": {"siapa"}}); w.Code != http.StatusNotFound {
		t.Errorf("user ga ada: dapat %d, harusnya 404", w.Code)
	}
}

// nonaktifin nyabut session dan nolak login, aktifin lagi bisa login
func TestAdminDeactivateAndActivate(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	adminID := insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	admin := login(r, "admin")
	login(r, "budi")

	w := adminPost(t, r, admin, "/admin/users/"+itoa(budiID)+"/deactivate", nil)
	if w.Code != http.StatusSeeOther || activeSessions(t, db, budiID) != 0 {
		t.Errorf("nonaktifin: status %d, session harus dicabut", w.Code)
	}
	if login(r, "budi") != nil {
		t.Error("akun nonaktif ga boleh bisa login")
	}

	adminPost(t, r, admin, "/admin/users/"+itoa(budiID)+"/activate", nil)
	if login(r, "budi") == nil {
		t.Error("akun yang diaktifin lagi harus bisa login")
	}

	if w := adminPost(t, r, admin, "/admin/users/"+itoa(adminID)+"/deactivate", nil); w.Code != http.StatusBadRequest {
		t.Errorf("nonaktifin diri sendiri: dapat %d, harusnya 400", w.Code)
	}
}

// ubah id ke string buat nyusun URL
func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
