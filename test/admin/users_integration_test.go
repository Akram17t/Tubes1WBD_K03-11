package admin_test

import (
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"
	testdb "tubes-wbd/test/database"

	"tubes-wbd/src/admin"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// buka test-db dan kosongin tabel, skip kalau TEST_DATABASE_URL ga diisi
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testdb.Open(t)
}

// router lengkap kayak di aplikasi: auth dulu, baru route admin
func newAdminRouter(db *sql.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth.RegisterRoutes(r, db, auth.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		AbsoluteTimeout:  8 * time.Hour,
		RotationInterval: 15 * time.Minute,
		GracePeriod:      30 * time.Second,
	})
	admin.RegisterRoutes(r, db)
	return r
}

// bikin user dengan password beneran, balikin id-nya
func insertUser(t *testing.T, db *sql.DB, username string, isAdmin bool) int64 {
	t.Helper()
	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = db.QueryRow(`
		INSERT INTO users (username, password_hash, display_name, is_platform_admin)
		VALUES ($1, $2, $1, $3) RETURNING id
	`, username, hash, isAdmin).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// hitung session yang masih aktif punya user
func activeSessions(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var count int
	db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&count)
	return count
}

// user yang jadi leader lalu dinonaktifin harus dicopot dari jabatan leader-nya
func TestAdminDeactivateClearsGroupLeader(t *testing.T) {
	db := openTestDB(t)
	r := newAdminRouter(db)
	insertUser(t, db, "admin", true)
	budiID := insertUser(t, db, "budi", false)
	groupID := insertGroup(t, db, "tim-a")
	mustExec(t, db, `INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)`, budiID, groupID)
	mustExec(t, db, `UPDATE groups SET leader_user_id = $1 WHERE id = $2`, budiID, groupID)
	admin := apiLogin(t, r, "admin")

	if w, _ := callAPI(r, http.MethodPatch, "/api/admin/users/"+itoa(budiID), `{"is_active":false}`, admin); w.Code != http.StatusOK {
		t.Fatalf("nonaktifin: dapat %d, harusnya 200", w.Code)
	}
	var leader sql.NullInt64
	if err := db.QueryRow(`SELECT leader_user_id FROM groups WHERE id = $1`, groupID).Scan(&leader); err != nil {
		t.Fatal(err)
	}
	if leader.Valid {
		t.Errorf("leader_user_id harusnya NULL, dapat %d", leader.Int64)
	}
}

// ubah id ke string buat nyusun URL
func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
