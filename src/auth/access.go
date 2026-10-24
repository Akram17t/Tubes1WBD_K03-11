package auth

import (
	"context"
	"database/sql"
	"errors"
)

// Semua aturan izin ada di file ini. Modul lain cukup memanggil Authorize.

type Action string

const (
	ViewSchema       Action = "view_schema"
	ViewData         Action = "view_data"
	ViewMigration    Action = "view_migration"
	CreateMigration  Action = "create_migration"
	EditMigration    Action = "edit_migration"
	ExecuteMigration Action = "execute_migration"
	ManagePlatform   Action = "manage_platform"
)

// Level berurutan supaya edit otomatis mencakup view (edit >= view)
type Level int

const (
	LevelNone Level = iota
	LevelView
	LevelEdit
)

var ErrForbidden = errors.New("forbidden")

// tingkat akses minimum untuk tiap aksi database
var requiredLevel = map[Action]Level{
	ViewSchema:       LevelView,
	ViewData:         LevelView,
	ViewMigration:    LevelView,
	CreateMigration:  LevelEdit,
	EditMigration:    LevelEdit,
	ExecuteMigration: LevelEdit,
}

// cek level user cukup buat aksi ini, aksi yang ga dikenal langsung ditolak
func allowed(level Level, action Action) bool {
	minimum, known := requiredLevel[action]
	if !known {
		return false
	}
	return level >= minimum
}

// cari hak tertinggi user ke satu database dari semua group-nya
func EffectiveLevel(ctx context.Context, db *sql.DB, userID, dbID int64) (Level, error) {
	var level sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT MAX(CASE gda.access_level WHEN 'edit' THEN 2 WHEN 'view' THEN 1 END)
		FROM user_groups ug
		JOIN group_database_access gda ON gda.group_id = ug.group_id
		JOIN database_connections dc ON dc.id = gda.database_id
		WHERE ug.user_id = $1 AND gda.database_id = $2 AND dc.is_active
	`, userID, dbID).Scan(&level)
	if err != nil {
		return LevelNone, err
	}

	// MAX dari nol baris hasilnya NULL, artinya ga punya akses
	if !level.Valid {
		return LevelNone, nil
	}
	return Level(level.Int64), nil
}

// pintu utama cek izin, balikin ErrForbidden kalau ditolak.
// dbID diabaikan untuk ManagePlatform.
func Authorize(ctx context.Context, db *sql.DB, user *User, action Action, dbID int64) error {
	if user == nil {
		return ErrForbidden
	}

	// Platform Admin ngatur platform, bukan otomatis punya akses database
	if action == ManagePlatform {
		if user.IsPlatformAdmin {
			return nil
		}
		return ErrForbidden
	}

	level, err := EffectiveLevel(ctx, db, user.ID, dbID)
	if err != nil {
		return err
	}
	if !allowed(level, action) {
		return ErrForbidden
	}
	return nil
}
