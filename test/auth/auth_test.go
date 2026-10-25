package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	. "tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// bikin router kecil dengan user palsu (boleh nil) dan middleware yang mau dites
func newTestRouter(user *User, middlewares ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			c.Set("auth.user", user)
		}
		c.Next()
	})
	r.Use(middlewares...)
	r.GET("/x", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

// kirim GET /x lalu balikin status code-nya
func statusOf(r *gin.Engine) (int, string) {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w.Code, w.Header().Get("Location")
}

// cek RequireLogin: belum login diarahkan ke /login, sudah login lolos
func TestRequireLogin(t *testing.T) {
	code, location := statusOf(newTestRouter(nil, RequireLogin()))
	if code != http.StatusSeeOther || location != "/login" {
		t.Errorf("belum login: dapat %d ke %q, harusnya 303 ke /login", code, location)
	}

	code, _ = statusOf(newTestRouter(&User{ID: 1}, RequireLogin()))
	if code != http.StatusOK {
		t.Errorf("sudah login: dapat %d, harusnya 200", code)
	}
}

// cek RequireAdmin: non-admin 403, admin lolos
func TestRequireAdmin(t *testing.T) {
	code, _ := statusOf(newTestRouter(&User{ID: 1}, RequireLogin(), RequireAdmin()))
	if code != http.StatusForbidden {
		t.Errorf("non-admin: dapat %d, harusnya 403", code)
	}

	code, _ = statusOf(newTestRouter(&User{ID: 1, IsPlatformAdmin: true}, RequireLogin(), RequireAdmin()))
	if code != http.StatusOK {
		t.Errorf("admin: dapat %d, harusnya 200", code)
	}
}
