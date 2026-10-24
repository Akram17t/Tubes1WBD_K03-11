package auth

import (
	"context"
	"errors"
	"testing"
)

// cek tabel izin: edit mencakup view, view ga boleh ngubah, none ga boleh apa-apa
func TestAllowed(t *testing.T) {
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
		got := allowed(tt.level, tt.action)
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
