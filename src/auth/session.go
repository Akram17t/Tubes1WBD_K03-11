package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// SessionConfig berisi aturan umur session, diisi dari env oleh main
type SessionConfig struct {
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
	SecureCookie     bool
}

const (
	sessionCookieName = "session"
	sessionKey        = "auth.session"
)

// hash token pakai SHA-256, database cuma nyimpen hasil ini
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// bikin session baru plus token pertamanya, balikin token asli buat cookie
func createSession(ctx context.Context, db *sql.DB, userID int64, privilegeVersion int, config SessionConfig) (string, error) {
	token := rand.Text()
	csrfSecret := make([]byte, 32)
	rand.Read(csrfSecret)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var sessionID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO sessions (user_id, absolute_expires_at, privilege_version, csrf_secret)
		VALUES ($1, now() + make_interval(secs => $2), $3, $4)
		RETURNING id
	`, userID, config.AbsoluteTimeout.Seconds(), privilegeVersion, csrfSecret).Scan(&sessionID)
	if err != nil {
		return "", err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_tokens (token_hash, session_id) VALUES ($1, $2)
	`, hashToken(token), sessionID)
	if err != nil {
		return "", err
	}

	return token, tx.Commit()
}

// cari session dari token cookie, sql.ErrNoRows kalau token ga valid,
// kadaluarsa, dicabut, atau user-nya nonaktif
func findSession(ctx context.Context, db *sql.DB, token string, config SessionConfig) (int64, *User, error) {
	var sessionID int64
	user := &User{}
	err := db.QueryRowContext(ctx, `
		SELECT s.id, u.id, u.username, u.display_name, u.is_platform_admin
		FROM session_tokens st
		JOIN sessions s ON s.id = st.session_id
		JOIN users u ON u.id = s.user_id
		WHERE st.token_hash = $1
		  AND (st.grace_expires_at IS NULL OR st.grace_expires_at > now())
		  AND s.revoked_at IS NULL
		  AND s.absolute_expires_at > now()
		  AND s.last_activity_at > now() - make_interval(secs => $2)
		  AND u.is_active
	`, hashToken(token), config.IdleTimeout.Seconds()).Scan(
		&sessionID, &user.ID, &user.Username, &user.DisplayName, &user.IsPlatformAdmin,
	)
	if err != nil {
		return 0, nil, err
	}
	return sessionID, user, nil
}

// catat aktivitas terakhir biar idle timeout keitung dari request terakhir
func touchSession(ctx context.Context, db *sql.DB, sessionID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET last_activity_at = now() WHERE id = $1`, sessionID)
	return err
}

// cabut satu session, dipakai waktu logout
func revokeSession(ctx context.Context, db *sql.DB, sessionID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL
	`, sessionID)
	return err
}

// ambil id session yang lagi dipakai request ini
func currentSessionID(c *gin.Context) (int64, bool) {
	value, exists := c.Get(sessionKey)
	if !exists {
		return 0, false
	}
	sessionID, ok := value.(int64)
	return sessionID, ok
}

// kirim token ke browser lewat cookie HttpOnly
func setSessionCookie(c *gin.Context, token string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// hapus cookie session di browser
func clearSessionCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// middleware yang baca cookie dan naruh user login ke context.
// Request tanpa session yang valid tetap lanjut, yang nolak itu RequireLogin.
func (h *Handler) Session() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookieName)
		if err != nil || token == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		sessionID, user, err := findSession(ctx, h.DB, token, h.Config)
		if errors.Is(err, sql.ErrNoRows) {
			clearSessionCookie(c, h.Config.SecureCookie)
			c.Next()
			return
		}
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		if err := touchSession(ctx, h.DB, sessionID); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		c.Set(sessionKey, sessionID)
		setCurrentUser(c, user)
		c.Next()
	}
}

// naikin privilege_version user, nanti session-nya dirotasi di request berikutnya.
func BumpPrivilege(ctx context.Context, tx *sql.Tx, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE users
		SET privilege_version = privilege_version + 1, updated_at = now()
		WHERE id = ANY($1)
	`, userIDs)
	return err
}

// cabut semua session aktif milik user, mereka harus login ulang.
func RevokeSessions(ctx context.Context, tx *sql.Tx, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = now()
		WHERE user_id = ANY($1) AND revoked_at IS NULL
	`, userIDs)
	return err
}
