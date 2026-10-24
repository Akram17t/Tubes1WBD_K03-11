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

// activeSession adalah session valid yang dititipkan middleware ke context
type activeSession struct {
	ID            int64
	CSRFSecret    []byte
	needsRotation bool
}

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
// kadaluarsa, dicabut, atau user-nya nonaktif.
// needsRotation true kalau token ini yang current dan sudah waktunya dirotasi
// (lewat interval rotasi atau privilege user naik)
func findSession(ctx context.Context, db *sql.DB, token string, config SessionConfig) (*activeSession, *User, error) {
	session := &activeSession{}
	user := &User{}
	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.csrf_secret, u.id, u.username, u.display_name, u.is_platform_admin,
		       st.grace_expires_at IS NULL AND (
		           s.last_rotated_at <= now() - make_interval(secs => $3)
		           OR s.privilege_version <> u.privilege_version
		       )
		FROM session_tokens st
		JOIN sessions s ON s.id = st.session_id
		JOIN users u ON u.id = s.user_id
		WHERE st.token_hash = $1
		  AND (st.grace_expires_at IS NULL OR st.grace_expires_at > now())
		  AND s.revoked_at IS NULL
		  AND s.absolute_expires_at > now()
		  AND s.last_activity_at > now() - make_interval(secs => $2)
		  AND u.is_active
	`, hashToken(token), config.IdleTimeout.Seconds(), config.RotationInterval.Seconds()).Scan(
		&session.ID, &session.CSRFSecret, &user.ID, &user.Username, &user.DisplayName, &user.IsPlatformAdmin,
		&session.needsRotation,
	)
	if err != nil {
		return nil, nil, err
	}
	return session, user, nil
}

// catat aktivitas terakhir biar idle timeout keitung dari request terakhir
func touchSession(ctx context.Context, db *sql.DB, sessionID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET last_activity_at = now() WHERE id = $1`, sessionID)
	return err
}

// ganti token session: token lama dikasih grace period, token baru jadi current.
// rotated false kalau request lain sudah ngerotasi duluan, token lama tetap jalan selama grace.
// absolute_expires_at sengaja ga disentuh biar umur session ga nambah
func rotateSession(ctx context.Context, db *sql.DB, sessionID int64, oldToken string, config SessionConfig) (string, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()

	// kunci session, request lain yang mau rotasi session ini harus nunggu
	var lockedID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id = $1 FOR UPDATE`, sessionID).Scan(&lockedID)
	if err != nil {
		return "", false, err
	}

	// token lama masuk grace period, tapi cuma kalau masih current
	result, err := tx.ExecContext(ctx, `
		UPDATE session_tokens
		SET grace_expires_at = now() + make_interval(secs => $3)
		WHERE token_hash = $1 AND session_id = $2 AND grace_expires_at IS NULL
	`, hashToken(oldToken), sessionID, config.GracePeriod.Seconds())
	if err != nil {
		return "", false, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return "", false, nil
	}

	newToken := rand.Text()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_tokens (token_hash, session_id) VALUES ($1, $2)
	`, hashToken(newToken), sessionID)
	if err != nil {
		return "", false, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET last_rotated_at = now(),
		    privilege_version = (SELECT privilege_version FROM users WHERE id = sessions.user_id)
		WHERE id = $1
	`, sessionID)
	if err != nil {
		return "", false, err
	}

	// buang token lama yang grace-nya sudah habis biar tabel ga numpuk
	_, err = tx.ExecContext(ctx, `
		DELETE FROM session_tokens WHERE session_id = $1 AND grace_expires_at <= now()
	`, sessionID)
	if err != nil {
		return "", false, err
	}

	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return newToken, true, nil
}

// cabut satu session, dipakai waktu logout
func revokeSession(ctx context.Context, db *sql.DB, sessionID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL
	`, sessionID)
	return err
}

// ambil session yang lagi dipakai request ini
func currentSession(c *gin.Context) (*activeSession, bool) {
	value, exists := c.Get(sessionKey)
	if !exists {
		return nil, false
	}
	session, ok := value.(*activeSession)
	return session, ok
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

// middleware yang baca cookie, ngerotasi token kalau sudah waktunya, dan naruh user login ke context.
// Request tanpa session yang valid tetap lanjut, yang nolak itu RequireLogin.
func (h *Handler) Session() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookieName)
		if err != nil || token == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		session, user, err := findSession(ctx, h.DB, token, h.Config)
		if errors.Is(err, sql.ErrNoRows) {
			clearSessionCookie(c, h.Config.SecureCookie)
			c.Next()
			return
		}
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		if err := touchSession(ctx, h.DB, session.ID); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		if session.needsRotation {
			newToken, rotated, err := rotateSession(ctx, h.DB, session.ID, token, h.Config)
			if err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			if rotated {
				setSessionCookie(c, newToken, h.Config.SecureCookie)
			}
		}

		c.Set(sessionKey, session)
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
