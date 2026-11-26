package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// groupJSONRequest mengirim JSON dengan cookie dan header CSRF seperti SPA.
func groupJSONRequest(r *gin.Engine, method, path, body, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", token)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestGroupAPIUsesSharedServicesAndResponses menjaga leader, PATCH, CSRF, dan pagination setelah helper disatukan.
func TestGroupAPIUsesSharedServicesAndResponses(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	adminID := insertUser(t, db, "admin", true)
	memberID := insertUser(t, db, "member", false)
	groupID := insertGroup(t, db, "Developer")
	databaseID := insertConnection(t, db, "Target API", adminID)
	adminSession, memberSession := apiLogin(t, r, "admin"), apiLogin(t, r, "member")
	admin, member, token := adminSession.cookie, memberSession.cookie, adminSession.csrf
	path := "/api/admin/groups/" + idString(groupID)
	for _, tc := range []struct {
		method, path, body, csrf string
		cookie                   *http.Cookie
		status                   int
	}{
		{"GET", path, "", "", nil, 401},
		{"GET", path, "", "", member, 403},
		{"PATCH", path, `{}`, "", admin, 403},
		{"PATCH", path, `{"unknown":true}`, token, admin, 400},
		{"PATCH", path, `{"leader_user_id":"` + idString(memberID) + `"}`, token, admin, 400},
		{"POST", path + "/members", `{"user_id":"` + idString(memberID) + `"}`, token, admin, 201},
		{"PATCH", path, `{"leader_user_id":"` + idString(memberID) + `","description":"demo"}`, token, admin, 200},
		{"PATCH", path, `{"description":null}`, token, admin, 200},
		{"PUT", path + "/access/" + idString(databaseID), `{"access_level":"view"}`, token, admin, 200},
		{"GET", path + "/access?limit=1", "", "", admin, 200},
		{"GET", "/api/admin/groups?limit=", "", "", admin, 400},
	} {
		w := groupJSONRequest(r, tc.method, tc.path, tc.body, tc.csrf, tc.cookie)
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s %s: %d %s, ingin %d", tc.method, tc.path, w.Code, w.Body.String(), tc.status)
		}
	}
	w := groupJSONRequest(r, "GET", path, "", "", admin)
	if !strings.Contains(w.Body.String(), `"name":"Developer"`) ||
		!strings.Contains(w.Body.String(), `"leader_user_id":"`+idString(memberID)+`"`) ||
		!strings.Contains(w.Body.String(), `"description":null`) {
		t.Fatalf("PATCH nullable tidak boleh menimpa nama/leader: %s", w.Body.String())
	}
	w = groupJSONRequest(r, "GET", path+"/members?limit=1", "", "", admin)
	if !strings.Contains(w.Body.String(), `"is_leader":true`) || !strings.Contains(w.Body.String(), `"has_more":false`) {
		t.Fatalf("DTO anggota/pagination salah: %s", w.Body.String())
	}
	w = groupJSONRequest(r, "DELETE", path+"/members/"+idString(memberID), "", token, admin)
	if w.Code != 200 {
		t.Fatalf("hapus anggota: %d %s", w.Code, w.Body.String())
	}
	var leader *int64
	if err := db.QueryRow(`SELECT leader_user_id FROM groups WHERE id=$1`, groupID).Scan(&leader); err != nil || leader != nil {
		t.Fatalf("menghapus anggota harus mengosongkan leader: %v, %v", leader, err)
	}
}
