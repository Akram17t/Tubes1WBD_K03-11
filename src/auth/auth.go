package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
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

// hash palsu buat dibandingin kalau username ga ketemu, biar waktu responnya sama
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

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

// tendang ke /login kalau belum login
func RequireLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := CurrentUser(c); !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// cuma Platform Admin yang boleh lewat, pasang setelah RequireLogin
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || !user.IsPlatformAdmin {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

// ubah password jadi hash bcrypt, dipakai juga waktu admin bikin atau ganti password user
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
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

// tampilin form login
func (h *Handler) showLogin(c *gin.Context) {
	if _, ok := CurrentUser(c); ok {
		c.Redirect(http.StatusSeeOther, "/databases")
		return
	}
	renderLogin(c, http.StatusOK, "", "")
}

// cek username dan password, kalau cocok bikin session baru
func (h *Handler) login(c *gin.Context) {
	ctx := c.Request.Context()
	username := NormalizeUsername(c.PostForm("username"))
	password := c.PostForm("password")

	userID, privilegeVersion, err := h.checkCredentials(ctx, username, password)
	if errors.Is(err, errInvalidLogin) {
		renderLogin(c, http.StatusUnauthorized, errInvalidLogin.Error(), username)
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// session lama di browser ini dicabut, login selalu dapat identifier baru
	if session, ok := currentSession(c); ok {
		if err := revokeSession(ctx, h.DB, session.ID); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	token, err := createSession(ctx, h.DB, userID, privilegeVersion, h.Config)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	setSessionCookie(c, token, h.Config.SecureCookie)
	c.Redirect(http.StatusSeeOther, "/databases")
}

// cocokin password, semua alasan gagal dibalikin sebagai errInvalidLogin biar pesannya sama
func (h *Handler) checkCredentials(ctx context.Context, username, password string) (int64, int, error) {
	var (
		userID           int64
		passwordHash     string
		isActive         bool
		privilegeVersion int
	)
	err := h.DB.QueryRowContext(ctx, `
		SELECT id, password_hash, is_active, privilege_version
		FROM users WHERE username = $1
	`, username).Scan(&userID, &passwordHash, &isActive, &privilegeVersion)

	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return 0, 0, errInvalidLogin
	}
	if err != nil {
		return 0, 0, err
	}

	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return 0, 0, errInvalidLogin
	}
	if !isActive {
		return 0, 0, errInvalidLogin
	}
	return userID, privilegeVersion, nil
}

// cabut session di backend, hapus cookie, balik ke login
func (h *Handler) logout(c *gin.Context) {
	if session, ok := currentSession(c); ok {
		if err := revokeSession(c.Request.Context(), h.DB, session.ID); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}
	clearSessionCookie(c, h.Config.SecureCookie)
	c.Redirect(http.StatusSeeOther, "/login")
}

// render halaman login dengan pesan error (kosong kalau ga ada)
func renderLogin(c *gin.Context, status int, errorMessage, username string) {
	c.HTML(status, "login.html", gin.H{
		"Error":    errorMessage,
		"Username": username,
	})
}
