package admin_test

import (
	"net/http"
	"testing"
)

// API group pakai token CSRF dari auth: tanpa token ditolak, token dari login diterima
func TestGroupAPIUsesSharedCSRFToken(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	admin := apiLogin(t, r, "admin")

	noToken := apiSession{cookie: admin.cookie}
	if w, body := callAPI(r, http.MethodPost, "/api/admin/groups", `{"name":"analyst"}`, noToken); w.Code != http.StatusForbidden || codeOf(body) != "csrf_invalid" {
		t.Errorf("tanpa token CSRF: dapat %d %s, harusnya 403 csrf_invalid", w.Code, codeOf(body))
	}
	if w, _ := callAPI(r, http.MethodPost, "/api/admin/groups", `{"name":"analyst"}`, admin); w.Code != http.StatusCreated {
		t.Errorf("bikin group pakai token dari login: dapat %d, harusnya 201", w.Code)
	}

	var count int
	db.QueryRow(`SELECT count(*) FROM groups WHERE name = 'analyst'`).Scan(&count)
	if count != 1 {
		t.Error("group harusnya tersimpan sekali")
	}
}
