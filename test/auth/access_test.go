package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	. "tubes-wbd/src/auth"
)

// cek tabel izin: edit mencakup view, view ga boleh ngubah, none ga boleh apa-apa
func TestAllowed(t *testing.T) {

	db := openTestDB(t)
	users := map[Level]int64{}
	for _, level := range []Level{LevelNone, LevelView, LevelEdit} {
		users[level] = insertUser(t, db, fmt.Sprintf("level-%d", level))
	}
	var databaseID int64
	if err := db.QueryRow(`INSERT INTO database_connections
        (name,host,database_name,db_username,password_encrypted,ssl_mode,created_by)
        VALUES ('test','localhost','test','u','x','disable',$1) RETURNING id`, users[LevelEdit]).Scan(&databaseID); err != nil {
		t.Fatal(err)
	}
	for _, level := range []Level{LevelView, LevelEdit} {
		groupID := insertGroup(t, db, fmt.Sprintf("group-%d", level))
		access := "view"
		if level == LevelEdit {
			access = "edit"
		}
		mustExec(t, db, `INSERT INTO user_groups (user_id,group_id) VALUES ($1,$2)`, users[level], groupID)
		mustExec(t, db, `INSERT INTO group_database_access (group_id,database_id,access_level,granted_by) VALUES ($1,$2,$3,$4)`, groupID, databaseID, access, users[LevelEdit])
	}
	tests := []struct {
		level  Level
		action Action
		want   bool
	}{
		{LevelView, ViewSchema, true},
		{LevelView, ViewData, true},
		{LevelView, ViewMigration, true},
		{LevelView, CreateMigration, false},
		{LevelView, EditMigration, false},
		{LevelView, ExecuteMigration, false},
		{LevelEdit, ViewSchema, true},
		{LevelEdit, ExecuteMigration, true},
		{LevelNone, ViewSchema, false},
		{LevelEdit, Action("hapus_semua"), false},
		{LevelEdit, ManagePlatform, false},
	}

	for _, tt := range tests {
		err := Authorize(context.Background(), db, &User{ID: users[tt.level]}, tt.action, databaseID)
		got := err == nil
		if err != nil && !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("allowed(%v, %q) = %v, want %v", tt.level, tt.action, got, tt.want)
		}
	}
}

// cek ManagePlatform cuma ngeliat status admin, tanpa nyentuh database
func TestAuthorizeManagePlatform(t *testing.T) {
	ctx := context.Background()
	admin := &User{ID: 1, IsPlatformAdmin: true}
	biasa := &User{ID: 2}

	if err := Authorize(ctx, nil, admin, ManagePlatform, 0); err != nil {
		t.Errorf("admin harusnya boleh, dapat %v", err)
	}
	if err := Authorize(ctx, nil, biasa, ManagePlatform, 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("user biasa harusnya ErrForbidden, dapat %v", err)
	}
	if err := Authorize(ctx, nil, nil, ViewSchema, 1); !errors.Is(err, ErrForbidden) {
		t.Errorf("user nil harusnya ErrForbidden, dapat %v", err)
	}
}
