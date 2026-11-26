package admin_test

import (
	"database/sql"
	"net/http"
	"strconv"
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

// user biasa ga boleh buka API group dan akses database
func TestGroupRoutesRejectNonAdmin(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "analyst")
	budi := apiLogin(t, r, "budi")

	paths := []string{
		"/api/admin/groups",
		"/api/admin/groups/" + idString(groupID),
		"/api/admin/groups/" + idString(groupID) + "/members",
		"/api/admin/groups/" + idString(groupID) + "/access",
	}
	for _, path := range paths {
		if w, body := callAPI(r, http.MethodGet, path, "", budi); w.Code != http.StatusForbidden || codeOf(body) != "forbidden" {
			t.Errorf("GET %s oleh user biasa: dapat %d, harusnya 403", path, w.Code)
		}
	}
}

// bikin, ubah, hapus group plus input jelek dan group yang ga ada
func TestGroupCRUDValidationAndNotFound(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	admin := apiLogin(t, r, "admin")

	w, body := callAPI(r, http.MethodPost, "/api/admin/groups", `{"name":"analyst"}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("bikin group: dapat %d, harusnya 201", w.Code)
	}
	groupID := dataOf(body)["id"].(string)

	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"nama kosong", http.MethodPost, "/api/admin/groups", `{"name":"  "}`, http.StatusBadRequest},
		{"nama dobel", http.MethodPost, "/api/admin/groups", `{"name":"analyst"}`, http.StatusConflict},
		{"update group ga ada", http.MethodPatch, "/api/admin/groups/999", `{"name":"x"}`, http.StatusNotFound},
		{"id bukan angka", http.MethodPatch, "/api/admin/groups/abc", `{"name":"x"}`, http.StatusBadRequest},
		{"hapus group ga ada", http.MethodDelete, "/api/admin/groups/999", "", http.StatusNotFound},
		{"detail group ga ada", http.MethodGet, "/api/admin/groups/999", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		if w, _ := callAPI(r, tt.method, tt.path, tt.body, admin); w.Code != tt.want {
			t.Errorf("%s: dapat %d, harusnya %d", tt.name, w.Code, tt.want)
		}
	}

	path := "/api/admin/groups/" + groupID
	if w, _ := callAPI(r, http.MethodPatch, path, `{"name":"data-analyst"}`, admin); w.Code != http.StatusOK {
		t.Errorf("update group: dapat %d, harusnya 200", w.Code)
	}
	if w, _ := callAPI(r, http.MethodDelete, path, "", admin); w.Code != http.StatusOK {
		t.Errorf("hapus group: dapat %d, harusnya 200", w.Code)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM groups WHERE id=$1 AND is_archived AND leader_user_id IS NULL`, groupID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("group harusnya diarsipkan dan leader dikosongkan")
	}
}

// tambah anggota naikin privilege, anggota dobel 409, keluarin anggota nyabut session
func TestGroupMembersPrivilegeAndSessions(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "analyst")
	admin := apiLogin(t, r, "admin")
	apiLogin(t, r, "budi")
	members := "/api/admin/groups/" + idString(groupID) + "/members"
	add := func(userID string) int {
		w, _ := callAPI(r, http.MethodPost, members, `{"user_id":"`+userID+`"}`, admin)
		return w.Code
	}

	if code := add(idString(budiID)); code != http.StatusCreated {
		t.Fatalf("tambah anggota: dapat %d, harusnya 201", code)
	}
	if privilegeVersion(t, db, budiID) != 1 || activeSessions(t, db, budiID) != 1 {
		t.Error("tambah anggota harus naikin privilege_version tanpa nyabut session")
	}
	if code := add(idString(budiID)); code != http.StatusConflict {
		t.Errorf("anggota dobel: dapat %d, harusnya 409", code)
	}
	if code := add("999"); code != http.StatusNotFound {
		t.Errorf("user ga ada: dapat %d, harusnya 404", code)
	}
	if code := add("abc"); code != http.StatusBadRequest {
		t.Errorf("user_id bukan angka: dapat %d, harusnya 400", code)
	}

	remove := members + "/" + idString(budiID)
	if w, _ := callAPI(r, http.MethodDelete, remove, "", admin); w.Code != http.StatusOK {
		t.Fatalf("keluarin anggota: dapat %d, harusnya 200", w.Code)
	}
	if activeSessions(t, db, budiID) != 0 {
		t.Error("keluarin anggota harus nyabut session")
	}
	if w, _ := callAPI(r, http.MethodDelete, remove, "", admin); w.Code != http.StatusNotFound {
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
	admin := apiLogin(t, r, "admin")
	apiLogin(t, r, "budi")

	if w, _ := callAPI(r, http.MethodDelete, "/api/admin/groups/"+idString(groupID), "", admin); w.Code != http.StatusOK {
		t.Fatalf("hapus group: dapat %d, harusnya 200", w.Code)
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
	admin := apiLogin(t, r, "admin")
	access := "/api/admin/groups/" + idString(groupID) + "/access/"
	grant := func(databaseID int64, level string) int {
		w, _ := callAPI(r, http.MethodPut, access+idString(databaseID), `{"access_level":"`+level+`"}`, admin)
		return w.Code
	}

	if code := grant(targetID, "view"); code != http.StatusOK || privilegeVersion(t, db, budiID) != 1 {
		t.Errorf("kasih akses view: dapat %d, privilege_version harusnya naik", code)
	}
	if code := grant(targetID, "edit"); code != http.StatusOK || privilegeVersion(t, db, budiID) != 2 {
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

	apiLogin(t, r, "budi")
	if code := grant(targetID, "view"); code != http.StatusOK || activeSessions(t, db, budiID) != 0 {
		t.Errorf("turunin ke view: dapat %d, session anggota harus dicabut", code)
	}

	apiLogin(t, r, "budi")
	revoke := access + idString(targetID)
	if w, _ := callAPI(r, http.MethodDelete, revoke, "", admin); w.Code != http.StatusOK || activeSessions(t, db, budiID) != 0 {
		t.Errorf("cabut akses: dapat %d, session anggota harus dicabut", w.Code)
	}
	if w, _ := callAPI(r, http.MethodDelete, revoke, "", admin); w.Code != http.StatusNotFound {
		t.Errorf("cabut akses yang ga ada: dapat %d, harusnya 404", w.Code)
	}
}

// jalanin query setup yang ga butuh hasil
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
