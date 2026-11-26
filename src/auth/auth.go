package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"tubes-wbd/src/api"

	"github.com/gin-gonic/gin"
)

// User adalah user login yang dititipkan middleware session ke context Gin
type User struct {
	ID              int64
	Username        string
	DisplayName     string
	IsPlatformAdmin bool
}

// Handler nyimpen dependency buat route login, logout, dan middleware session
type Handler struct {
	DB     *sql.DB
	Config SessionConfig
}

const userKey = "auth.user"

var errInvalidLogin = errors.New("username atau password salah")

// buat nitipin user login ke context, dipanggil middleware session
func setCurrentUser(c *gin.Context, user *User) {
	c.Set(userKey, user)
}

// ambil user yang lagi login, ok false kalau belum login
func CurrentUser(c *gin.Context) (*User, bool) {
	value, exists := c.Get(userKey)
	if !exists {
		return nil, false
	}

	user, ok := value.(*User)
	if !ok || user == nil {
		return nil, false
	}
	return user, true
}

// belum login: JSON 401, ga pernah redirect ke halaman login (SPA yang ngurus)
func RequireLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := CurrentUser(c); !ok {
			api.Fail(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Silakan login terlebih dahulu.", nil)
			return
		}
		c.Next()
	}
}

// cuma Platform Admin yang boleh lewat, pasang setelah RequireLogin. Di /api balasannya JSON 403
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || !user.IsPlatformAdmin {
			abortForbidden(c, "Hanya Platform Admin yang boleh mengakses fitur ini.")
			return
		}
		c.Next()
	}
}

func abortForbidden(c *gin.Context, message string) {
	api.Fail(c, http.StatusForbidden, api.CodeForbidden, message, nil)
}

// detail error cuma ke log, client dapet 500 generik
func abortInternal(c *gin.Context, err error) {
	api.Internal(c, err)
}

// rapihin username biar "Admin " dan "admin" dianggap sama
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// bikin Platform Admin pertama dari env kalau belum ada admin sama sekali
func EnsureAdmin(ctx context.Context, db *sql.DB, username, password string) error {
	var adminExists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE is_platform_admin)`).Scan(&adminExists)
	if err != nil {
		return err
	}
	if adminExists {
		return nil
	}

	username = NormalizeUsername(username)
	if username == "" || password == "" {
		return fmt.Errorf("belum ada Platform Admin: isi ADMIN_USERNAME dan ADMIN_PASSWORD")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, display_name, is_platform_admin)
		VALUES ($1, $2, $1, true)
	`, username, hash)
	return err
}

// inti login API: cek password, cabut session lama di browser ini,
// bikin session baru, lalu pasang cookie. Gagal password selalu errInvalidLogin
func (h *Handler) signIn(c *gin.Context, username, password string) (*User, *activeSession, error) {
	ctx := c.Request.Context()
	user, privilegeVersion, err := h.checkCredentials(ctx, username, password)
	if err != nil {
		return nil, nil, err
	}

	// session lama di browser ini dicabut, login selalu dapat identifier baru
	if session, ok := currentSession(c); ok {
		if err := revokeSession(ctx, h.DB, session.ID); err != nil {
			return nil, nil, err
		}
	}

	token, session, err := createSession(ctx, h.DB, user.ID, privilegeVersion, h.Config)
	if err != nil {
		return nil, nil, err
	}
	setSessionCookie(c, token, h.Config.SecureCookie)
	return user, session, nil
}

// cocokin password, semua alasan gagal dibalikin sebagai errInvalidLogin biar pesannya sama
func (h *Handler) checkCredentials(ctx context.Context, username, password string) (*User, int, error) {
	var (
		user             User
		passwordHash     string
		isActive         bool
		privilegeVersion int
	)
	err := h.DB.QueryRowContext(ctx, `
		SELECT id, username, display_name, is_platform_admin, password_hash, is_active, privilege_version
		FROM users WHERE username = $1
	`, username).Scan(&user.ID, &user.Username, &user.DisplayName, &user.IsPlatformAdmin, &passwordHash, &isActive, &privilegeVersion)

	if errors.Is(err, sql.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return nil, 0, errInvalidLogin
	}
	if err != nil {
		return nil, 0, err
	}

	if !CheckPassword(passwordHash, password) {
		return nil, 0, errInvalidLogin
	}
	if !isActive {
		return nil, 0, errInvalidLogin
	}
	return &user, privilegeVersion, nil
}
