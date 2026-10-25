package admin_test

import (
	"net/http"
	"net/url"
	"testing"
)

// form group harus bawa token CSRF dari auth, kalau ga semua POST group bakal 403
func TestGroupFormUsesSharedCSRFToken(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	admin := login(r, "admin")

	page := send(r, http.MethodGet, "/admin/groups/new", nil, admin)
	match := csrfPattern.FindStringSubmatch(page.Body.String())
	if match == nil {
		t.Fatalf("halaman group baru ga punya token CSRF (status %d)", page.Code)
	}

	form := url.Values{"name": {"analyst"}, "csrf_token": {match[1]}}
	if w := send(r, http.MethodPost, "/admin/groups", form, admin); w.Code != http.StatusSeeOther {
		t.Errorf("bikin group pakai token dari halaman: dapat %d, harusnya 303", w.Code)
	}

	var count int
	db.QueryRow(`SELECT count(*) FROM groups WHERE name = 'analyst'`).Scan(&count)
	if count != 1 {
		t.Error("group harusnya tersimpan")
	}
}
