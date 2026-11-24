package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/telemetry"

	"github.com/gin-gonic/gin"
)

// LoginPath endpoint login API, dikecualikan dari cek token CSRF (dicek lewat Origin).
const LoginPath = "/api/auth/login"

// body POST /api/auth/login
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserResponse data user login yang dikirim ke SPA, plus token CSRF buat header X-CSRF-Token.
// Token session dan hash password ga pernah ikut dikirim
type UserResponse struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
	CSRFToken       string `json:"csrf_token"`
}

// daftarin route /api/auth/*, middleware session + CSRF sudah kepasang global dari RegisterRoutes
func registerAPIRoutes(router gin.IRouter, h *Handler) {
	router.POST(LoginPath, h.apiLogin)
	router.GET("/api/auth/me", RequireLogin(), h.apiMe)
	router.POST("/api/auth/logout", RequireLogin(), h.apiLogout)
}

// login lewat JSON: cocok -> set cookie session dan balikin user + token CSRF,
// gagal -> 401 dengan pesan yang sama buat semua alasan (anti user enumeration)
func (h *Handler) apiLogin(c *gin.Context) {
	var body loginRequest
	if !api.BindJSON(c, &body) {
		return
	}
	username := NormalizeUsername(body.Username)
	user, session, err := h.signIn(c, username, body.Password)
	if errors.Is(err, errInvalidLogin) {
		slog.WarnContext(c.Request.Context(), "auth.login_failed", "username", username)
		api.Fail(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Username atau password salah.", nil)
		return
	}
	if err != nil {
		api.Internal(c, err)
		return
	}

	// request ini belum punya actor (baru login), jadi dititip manual biar log & span-nya kebaca
	c.Request = c.Request.WithContext(telemetry.WithActor(c.Request.Context(), user.ID))
	slog.InfoContext(c.Request.Context(), "auth.login_success", "username", user.Username)
	api.JSON(c, http.StatusOK, newUserResponse(user, session))
}

// balikin user yang lagi login + token CSRF baru, dipakai SPA tiap kali halaman di-refresh
func (h *Handler) apiMe(c *gin.Context) {
	user, _ := CurrentUser(c)
	session, _ := currentSession(c)
	api.JSON(c, http.StatusOK, newUserResponse(user, session))
}

// cabut session di server dan hapus cookie, balasannya data null
func (h *Handler) apiLogout(c *gin.Context) {
	if session, ok := currentSession(c); ok {
		if err := revokeSession(c.Request.Context(), h.DB, session.ID); err != nil {
			api.Internal(c, err)
			return
		}
	}
	clearSessionCookie(c, h.Config.SecureCookie)
	slog.InfoContext(c.Request.Context(), "auth.logout")
	api.JSON(c, http.StatusOK, nil)
}

// susun response user, ID dikirim sebagai string sesuai kontrak
func newUserResponse(user *User, session *activeSession) UserResponse {
	return UserResponse{
		ID:              api.FormatID(user.ID),
		Username:        user.Username,
		DisplayName:     user.DisplayName,
		IsPlatformAdmin: user.IsPlatformAdmin,
		CSRFToken:       makeCSRFToken(session.CSRFSecret),
	}
}
