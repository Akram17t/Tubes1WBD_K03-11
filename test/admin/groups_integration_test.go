package admin_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// bikin group langsung di database, balikin id-nya
func insertGroup(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO groups (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// bikin koneksi database dummy (ga pernah dibuka), balikin id-nya
func insertConnection(t *testing.T, db *sql.DB, name string, createdBy int64) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO database_connections (name, host, database_name, db_username, password_encrypted, ssl_mode, created_by)
		VALUES ($1, 'localhost', $2, 'postgres', 'x', 'disable', $3) RETURNING id
	`, name, name, createdBy).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ambil privilege_version user
func privilegeVersion(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var version int
	db.QueryRow(`SELECT privilege_version FROM users WHERE id = $1`, userID).Scan(&version)
	return version
}

// ubah id ke string buat nyusun URL
func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// user biasa ga boleh buka halaman group dan akses database
func TestGroupRoutesRejectNonAdmin(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "analyst")
	budi := login(r, "budi")

	paths := []string{
		"/admin/groups",
		"/admin/groups/new",
		"/admin/groups/" + idString(groupID) + "/edit",
		"/admin/groups/" + idString(groupID) + "/database-access",
	}
	for _, path := range paths {
		if w := send(r, http.MethodGet, path, nil, budi); w.Code != http.StatusForbidden {
			t.Errorf("GET %s oleh user biasa: dapat %d, harusnya 403", path, w.Code)
		}
	}
}

// bikin, ubah, hapus group plus input jelek dan group yang ga ada
func TestGroupCRUDValidationAndNotFound(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	admin := login(r, "admin")

	if w := adminPost(t, r, admin, "/admin/groups", url.Values{"name": {"analyst"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("bikin group: dapat %d, harusnya 303", w.Code)
	}
	var groupID int64
	db.QueryRow(`SELECT id FROM groups WHERE name = 'analyst'`).Scan(&groupID)

	tests := []struct {
		name string
		path string
		form url.Values
		want int
	}{
		{"nama kosong", "/admin/groups", url.Values{"name": {"  "}}, http.StatusBadRequest},
		{"nama dobel", "/admin/groups", url.Values{"name": {"analyst"}}, http.StatusConflict},
		{"update group ga ada", "/admin/groups/999/update", url.Values{"name": {"x"}}, http.StatusNotFound},
		{"id bukan angka", "/admin/groups/abc/update", url.Values{"name": {"x"}}, http.StatusBadRequest},
		{"hapus group ga ada", "/admin/groups/999/delete", nil, http.StatusNotFound},
	}
	for _, tt := range tests {
		if w := adminPost(t, r, admin, tt.path, tt.form); w.Code != tt.want {
			t.Errorf("%s: dapat %d, harusnya %d", tt.name, w.Code, tt.want)
		}
	}

	if w := send(r, http.MethodGet, "/admin/groups/999/edit", nil, admin); w.Code != http.StatusNotFound {
		t.Errorf("edit group ga ada: dapat %d, harusnya 404", w.Code)
	}

	path := "/admin/groups/" + idString(groupID)
	if w := adminPost(t, r, admin, path+"/update", url.Values{"name": {"data-analyst"}}); w.Code != http.StatusSeeOther {
		t.Errorf("update group: dapat %d, harusnya 303", w.Code)
	}
	if w := adminPost(t, r, admin, path+"/delete", nil); w.Code != http.StatusSeeOther {
		t.Errorf("hapus group: dapat %d, harusnya 303", w.Code)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM groups`).Scan(&count)
	if count != 0 {
		t.Errorf("group harusnya sudah terhapus, masih ada %d", count)
	}
}

// tambah anggota naikin privilege, anggota dobel 409, keluarin anggota nyabut session
func TestGroupMembersPrivilegeAndSessions(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "analyst")
	admin := login(r, "admin")
	login(r, "budi")
	members := "/admin/groups/" + idString(groupID) + "/members"

	w := adminPost(t, r, admin, members, url.Values{"user_id": {idString(budiID)}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("tambah anggota: dapat %d, harusnya 303", w.Code)
	}
	if privilegeVersion(t, db, budiID) != 1 || activeSessions(t, db, budiID) != 1 {
		t.Error("tambah anggota harus naikin privilege_version tanpa nyabut session")
	}

	if w := adminPost(t, r, admin, members, url.Values{"user_id": {idString(budiID)}}); w.Code != http.StatusConflict {
		t.Errorf("anggota dobel: dapat %d, harusnya 409", w.Code)
	}
	if w := adminPost(t, r, admin, members, url.Values{"user_id": {"999"}}); w.Code != http.StatusNotFound {
		t.Errorf("user ga ada: dapat %d, harusnya 404", w.Code)
	}
	if w := adminPost(t, r, admin, members, url.Values{"user_id": {"abc"}}); w.Code != http.StatusBadRequest {
		t.Errorf("user_id bukan angka: dapat %d, harusnya 400", w.Code)
	}

	remove := members + "/" + idString(budiID) + "/remove"
	if w := adminPost(t, r, admin, remove, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("keluarin anggota: dapat %d, harusnya 303", w.Code)
	}
	if activeSessions(t, db, budiID) != 0 {
		t.Error("keluarin anggota harus nyabut session")
	}
	if w := adminPost(t, r, admin, remove, nil); w.Code != http.StatusNotFound {
		t.Errorf("keluarin yang bukan anggota: dapat %d, harusnya 404", w.Code)
	}
}

// hapus group nyabut session semua anggotanya
func TestDeleteGroupRevokesMemberSessions(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "analyst")
	mustExec(t, db, `INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)`, budiID, groupID)
	admin := login(r, "admin")
	login(r, "budi")

	if w := adminPost(t, r, admin, "/admin/groups/"+idString(groupID)+"/delete", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("hapus group: dapat %d, harusnya 303", w.Code)
	}
	if activeSessions(t, db, budiID) != 0 {
		t.Error("hapus group harus nyabut session anggotanya")
	}
}

// kasih atau naikin akses naikin privilege, turunin atau cabut akses nyabut session
func TestDatabaseAccessGrantAndRevoke(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	adminID := insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "analyst")
	targetID := insertConnection(t, db, "football", adminID)
	inactiveID := insertConnection(t, db, "arsip", adminID)
	mustExec(t, db, `UPDATE database_connections SET is_active = false WHERE id = $1`, inactiveID)
	mustExec(t, db, `INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)`, budiID, groupID)
	admin := login(r, "admin")
	access := "/admin/groups/" + idString(groupID) + "/database-access"
	grant := func(databaseID int64, level string) int {
		form := url.Values{"database_id": {idString(databaseID)}, "access_level": {level}}
		return adminPost(t, r, admin, access, form).Code
	}

	if code := grant(targetID, "view"); code != http.StatusSeeOther || privilegeVersion(t, db, budiID) != 1 {
		t.Errorf("kasih akses view: dapat %d, privilege_version harusnya naik", code)
	}
	if code := grant(targetID, "edit"); code != http.StatusSeeOther || privilegeVersion(t, db, budiID) != 2 {
		t.Errorf("naikin ke edit: dapat %d, privilege_version harusnya naik lagi", code)
	}

	tests := []struct {
		name       string
		databaseID int64
		level      string
		want       int
	}{
		{"level ngasal", targetID, "admin", http.StatusBadRequest},
		{"database ga ada", 999, "view", http.StatusNotFound},
		{"koneksi nonaktif", inactiveID, "view", http.StatusConflict},
	}
	for _, tt := range tests {
		if code := grant(tt.databaseID, tt.level); code != tt.want {
			t.Errorf("%s: dapat %d, harusnya %d", tt.name, code, tt.want)
		}
	}

	login(r, "budi")
	if code := grant(targetID, "view"); code != http.StatusSeeOther || activeSessions(t, db, budiID) != 0 {
		t.Errorf("turunin ke view: dapat %d, session anggota harus dicabut", code)
	}

	login(r, "budi")
	revoke := access + "/" + idString(targetID) + "/revoke"
	if w := adminPost(t, r, admin, revoke, nil); w.Code != http.StatusSeeOther || activeSessions(t, db, budiID) != 0 {
		t.Errorf("cabut akses: dapat %d, session anggota harus dicabut", w.Code)
	}
	if w := adminPost(t, r, admin, revoke, nil); w.Code != http.StatusNotFound {
		t.Errorf("cabut akses yang ga ada: dapat %d, harusnya 404", w.Code)
	}
}

func TestDatabaseAccessFormOnlyOffersUnassignedTargets(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	adminID := insertUser(t, db, "admin", true)
	groupID := insertGroup(t, db, "analyst")
	first := insertConnection(t, db, "football", adminID)
	second := insertConnection(t, db, "training", adminID)
	admin := login(r, "admin")
	path := "/admin/groups/" + idString(groupID) + "/database-access"
	form := url.Values{"database_id": {idString(first)}, "access_level": {"view"}}
	if w := adminPost(t, r, admin, path, form); w.Code != http.StatusSeeOther {
		t.Fatal("gagal menyiapkan akses group")
	}
	page := send(r, http.MethodGet, path, nil, admin)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `<option value="`+idString(first)+`"`) || !strings.Contains(page.Body.String(), `<option value="`+idString(second)+`"`) {
		t.Fatal("form tambah akses harus hanya menawarkan target yang belum diberikan")
	}
	form.Set("database_id", idString(second))
	if w := adminPost(t, r, admin, path, form); w.Code != http.StatusSeeOther {
		t.Fatal("gagal memberikan akses kedua")
	}
	page = send(r, http.MethodGet, path, nil, admin)
	if strings.Contains(page.Body.String(), `id="database_id"`) || !strings.Contains(page.Body.String(), `name="access_level"`) {
		t.Fatal("form tambah disembunyikan setelah semua target diberikan, perubahan akses tetap tersedia")
	}
}

// jalanin query setup yang ga butuh hasil
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
