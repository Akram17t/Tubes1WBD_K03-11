package auth_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	. "tubes-wbd/src/auth"
)

func TestCSRFTokenValidAndRandom(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	r.POST("/csrf-check", RequireLogin(), func(c *gin.Context) { c.Status(http.StatusOK) })
	insertUserWithPassword(t, db, "editor", "rahasia123")
	cookie := loginAs(t, r, "editor", "rahasia123")
	first, second := csrfTokenFor(t, r, cookie), csrfTokenFor(t, r, cookie)
	if first == second {
		t.Fatal("dua token harus berbeda karena nonce acak")
	}
	for _, token := range []string{first, second} {
		if w := send(r, "POST", "/csrf-check", url.Values{"csrf_token": {token}}, cookie); w.Code != 200 {
			t.Errorf("token session yang sama ditolak: %d", w.Code)
		}
	}
}

func TestCSRFTokenRejected(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	r.POST("/csrf-check", RequireLogin(), func(c *gin.Context) { c.Status(http.StatusOK) })
	insertUserWithPassword(t, db, "editor", "rahasia123")
	insertUserWithPassword(t, db, "viewer", "rahasia123")
	cookie := loginAs(t, r, "editor", "rahasia123")
	other := loginAs(t, r, "viewer", "rahasia123")
	raw, err := base64.RawURLEncoding.DecodeString(csrfTokenFor(t, r, cookie))
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	for _, token := range []string{"", "ini bukan token!!", "YWJj", base64.RawURLEncoding.EncodeToString(raw), csrfTokenFor(t, r, other)} {
		if w := send(r, "POST", "/csrf-check", url.Values{"csrf_token": {token}}, cookie); w.Code != 403 {
			t.Errorf("token palsu/rusak/session lain diterima: %d", w.Code)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRF())
	r.POST("/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	tests := []struct {
		name, origin, referer string
		want                  bool
	}{
		{"origin sendiri", "http://example.com", "", true},
		{"origin situs lain", "http://situs-jahat.com", "", false},
		{"origin null", "null", "", false},
		{"referer sendiri", "", "http://example.com/login", true},
		{"referer situs lain", "", "http://situs-jahat.com/x", false},
		{"tanpa keduanya", "", "", false},
	}
	for _, tc := range tests {
		req := httptest.NewRequest("POST", "/login", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		if tc.referer != "" {
			req.Header.Set("Referer", tc.referer)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if (w.Code == 200) != tc.want {
			t.Errorf("%s: status %d", tc.name, w.Code)
		}
	}
}
