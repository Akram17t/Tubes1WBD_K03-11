package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// POST tanpa token, token salah, atau token session lain ditolak 403, token benar diterima
func TestCSRFProtectsPost(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	insertUserWithPassword(t, db, "viewer", "rahasia123")

	editorCookie := loginAs(t, r, "editor", "rahasia123")
	viewerCookie := loginAs(t, r, "viewer", "rahasia123")
	viewerToken := csrfTokenFor(t, r, viewerCookie)

	rejected := []struct {
		name string
		form url.Values
	}{
		{"tanpa token", url.Values{}},
		{"token ngasal", url.Values{"csrf_token": {"token-ngasal"}}},
		{"token punya session lain", url.Values{"csrf_token": {viewerToken}}},
	}
	for _, tc := range rejected {
		w := send(r, http.MethodPost, "/logout", tc.form, editorCookie)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: dapat %d, harusnya 403", tc.name, w.Code)
		}
	}

	// GET ga butuh token
	if w := send(r, http.MethodGet, "/databases", nil, editorCookie); w.Code != http.StatusOK {
		t.Errorf("GET tanpa token: dapat %d, harusnya 200", w.Code)
	}

	// semua penolakan di atas ga boleh bikin editor ke-logout
	editorToken := csrfTokenFor(t, r, editorCookie)
	w := send(r, http.MethodPost, "/logout", url.Values{"csrf_token": {editorToken}}, editorCookie)
	if w.Code != http.StatusSeeOther {
		t.Errorf("token benar: dapat %d, harusnya 303", w.Code)
	}
}

// login dari form situs lain ditolak lewat pengecekan Origin
func TestCSRFLoginChecksOrigin(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")

	form := url.Values{"username": {"editor"}, "password": {"rahasia123"}}.Encode()

	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{"origin situs lain", "http://situs-jahat.com", http.StatusForbidden},
		{"tanpa origin dan referer", "", http.StatusForbidden},
		{"origin sendiri", "http://example.com", http.StatusSeeOther},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != tt.want {
			t.Errorf("%s: dapat %d, harusnya %d", tt.name, w.Code, tt.want)
		}
	}
}
