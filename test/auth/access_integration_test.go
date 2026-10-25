package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	. "tubes-wbd/src/auth"
	testdb "tubes-wbd/test/database"
)

// buka test-db dan kosongin tabel, skip kalau TEST_DATABASE_URL ga diisi.
// Schema dari folder sql/ harus sudah dijalankan di database ini.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testdb.Open(t)
}

// bikin user baru dan balikin id-nya
func insertUser(t *testing.T, db *sql.DB, username string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO users (username, password_hash, display_name)
		VALUES ($1, 'x', $1) RETURNING id
	`, username).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// bikin group baru dan balikin id-nya
func insertGroup(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`INSERT INTO groups (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// jalanin query setup yang ga butuh hasil
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// skenario demo: viewer, editor, multi (dua group), outsider, dan admin tanpa group
func TestEffectiveLevelAndAuthorize(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	admin := insertUser(t, db, "admin")
	viewer := insertUser(t, db, "viewer")
	editor := insertUser(t, db, "editor")
	multi := insertUser(t, db, "multi")
	outsider := insertUser(t, db, "outsider")
	mustExec(t, db, `UPDATE users SET is_platform_admin = true WHERE id = $1`, admin)

	var target int64
	err := db.QueryRow(`
		INSERT INTO database_connections
			(name, host, database_name, db_username, password_encrypted, ssl_mode, created_by)
		VALUES ('target', 'localhost', 'target', 'u', 'x', 'disable', $1)
		RETURNING id
	`, admin).Scan(&target)
	if err != nil {
		t.Fatal(err)
	}

	analyst := insertGroup(t, db, "analyst")
	developer := insertGroup(t, db, "developer")
	mustExec(t, db, `INSERT INTO group_database_access (group_id, database_id, access_level, granted_by)
		VALUES ($1, $3, 'view', $4), ($2, $3, 'edit', $4)`, analyst, developer, target, admin)
	mustExec(t, db, `INSERT INTO user_groups (user_id, group_id)
		VALUES ($1, $3), ($2, $4), ($5, $3), ($5, $4)`, viewer, editor, analyst, developer, multi)

	levels := map[string]struct {
		id   int64
		want Level
	}{
		"admin":    {admin, LevelNone},
		"viewer":   {viewer, LevelView},
		"editor":   {editor, LevelEdit},
		"multi":    {multi, LevelEdit},
		"outsider": {outsider, LevelNone},
	}
	for name, c := range levels {
		got, err := EffectiveLevel(ctx, db, c.id, target)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: level %v, harusnya %v", name, got, c.want)
		}
	}

	if err := Authorize(ctx, db, &User{ID: viewer}, ExecuteMigration, target); !errors.Is(err, ErrForbidden) {
		t.Errorf("viewer execute harusnya ErrForbidden, dapat %v", err)
	}
	if err := Authorize(ctx, db, &User{ID: editor}, ExecuteMigration, target); err != nil {
		t.Errorf("editor execute harusnya boleh, dapat %v", err)
	}
	if err := Authorize(ctx, db, &User{ID: admin, IsPlatformAdmin: true}, ViewSchema, target); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin tanpa group harusnya ErrForbidden, dapat %v", err)
	}

	// koneksi nonaktif bikin semua akses ke database itu hilang
	mustExec(t, db, `UPDATE database_connections SET is_active = false WHERE id = $1`, target)
	if got, _ := EffectiveLevel(ctx, db, editor, target); got != LevelNone {
		t.Errorf("koneksi nonaktif: level %v, harusnya LevelNone", got)
	}
}

// cek BumpPrivilege dan RevokeSessions cuma kena ke user yang dikasih
func TestBumpPrivilegeAndRevokeSessions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	kena := insertUser(t, db, "kena")
	aman := insertUser(t, db, "aman")
	mustExec(t, db, `INSERT INTO sessions (user_id, absolute_expires_at, privilege_version, csrf_secret)
		VALUES ($1, now() + interval '8h', 0, 'x'), ($2, now() + interval '8h', 0, 'x')`, kena, aman)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := BumpPrivilege(ctx, tx, []int64{kena}); err != nil {
		t.Fatal(err)
	}
	if err := RevokeSessions(ctx, tx, []int64{kena}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var version int
	var revoked bool
	for _, c := range []struct {
		id          int64
		wantVersion int
		wantRevoked bool
	}{
		{kena, 1, true},
		{aman, 0, false},
	} {
		db.QueryRow(`SELECT privilege_version FROM users WHERE id = $1`, c.id).Scan(&version)
		db.QueryRow(`SELECT revoked_at IS NOT NULL FROM sessions WHERE user_id = $1`, c.id).Scan(&revoked)
		if version != c.wantVersion || revoked != c.wantRevoked {
			t.Errorf("user %d: version %d revoked %v, harusnya %d %v",
				c.id, version, revoked, c.wantVersion, c.wantRevoked)
		}
	}
}
