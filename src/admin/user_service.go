package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"tubes-wbd/src/auth"
)

// Error dari service user, handler HTML dan JSON yang nerjemahin ke status HTTP masing-masing.
var (
	errUserNotFound   = errors.New("user tidak ditemukan")
	errUsernameTaken  = errors.New("username sudah dipakai")
	errSelfDemote     = errors.New("tidak bisa mencabut status admin milik sendiri")
	errSelfDeactivate = errors.New("tidak bisa menonaktifkan akun sendiri")
)

// validationError buat input yang salah format, pesannya aman ditampilkan ke user.
type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

// userRecord satu baris user, dipakai handler HTML maupun JSON.
type userRecord struct {
	ID              int64
	Username        string
	DisplayName     string
	IsPlatformAdmin bool
	IsActive        bool
}

// userInput data buat bikin user baru.
type userInput struct {
	Username        string
	DisplayName     string
	Password        string
	IsPlatformAdmin bool
}

// userPatch perubahan sebagian, field nil artinya ga diubah.
type userPatch struct {
	Username        *string
	DisplayName     *string
	Password        *string
	IsPlatformAdmin *bool
	IsActive        *bool
}

// userService nyimpen semua aturan bisnis administrasi user.
type userService struct {
	db *sql.DB
}

// ambil daftar user urut username. limit < 0 artinya ambil semua (dipakai halaman HTML lama).
// hasMore true kalau masih ada data setelah halaman ini
func (s userService) list(ctx context.Context, limit, offset int) ([]userRecord, bool, error) {
	var sqlLimit sql.NullInt64
	if limit >= 0 {
		// ambil 1 lebih buat tau masih ada halaman berikutnya atau nggak
		sqlLimit = sql.NullInt64{Int64: int64(limit) + 1, Valid: true}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, display_name, is_platform_admin, is_active
		FROM users ORDER BY username, id
		LIMIT $1 OFFSET $2
	`, sqlLimit, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	users := []userRecord{}
	for rows.Next() {
		var u userRecord
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsPlatformAdmin, &u.IsActive); err != nil {
			return nil, false, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := limit >= 0 && len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	return users, hasMore, nil
}

// ambil satu user, errUserNotFound kalau ID-nya ga ada
func (s userService) get(ctx context.Context, id int64) (userRecord, error) {
	u := userRecord{ID: id}
	err := s.db.QueryRowContext(ctx, `
		SELECT username, display_name, is_platform_admin, is_active FROM users WHERE id = $1
	`, id).Scan(&u.Username, &u.DisplayName, &u.IsPlatformAdmin, &u.IsActive)
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, errUserNotFound
	}
	return u, err
}

// validasi + hash password lalu simpan user baru. Username dobel jadi errUsernameTaken
func (s userService) create(ctx context.Context, input userInput) (userRecord, error) {
	input.Username = auth.NormalizeUsername(input.Username)
	input.DisplayName = defaultDisplayName(input.DisplayName, input.Username)
	if err := validateUser(input.Username, input.DisplayName, &input.Password, true); err != nil {
		return userRecord{}, err
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		return userRecord{}, err
	}

	u := userRecord{Username: input.Username, DisplayName: input.DisplayName, IsPlatformAdmin: input.IsPlatformAdmin, IsActive: true}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO users (username, display_name, password_hash, is_platform_admin)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, u.Username, u.DisplayName, hash, u.IsPlatformAdmin).Scan(&u.ID)
	if isDuplicate(err) {
		return userRecord{}, errUsernameTaken
	}
	return u, err
}

// terapin patch ke user dalam satu transaction, sekalian atur session-nya:
// ganti password / dicabut admin / dinonaktifkan -> session dicabut, dijadiin admin -> privilege naik
func (s userService) update(ctx context.Context, actorID, id int64, patch userPatch) (userRecord, error) {
	// hash di luar transaction biar lock baris user ga ketahan lama (Argon2id itu sengaja lambat)
	var newHash string
	if patch.Password != nil {
		if err := validatePassword(*patch.Password); err != nil {
			return userRecord{}, err
		}
		hash, err := auth.HashPassword(*patch.Password)
		if err != nil {
			return userRecord{}, err
		}
		newHash = hash
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return userRecord{}, err
	}
	defer tx.Rollback()

	// kunci baris user biar ga ada admin lain yang ngubah barengan
	old := userRecord{ID: id}
	err = tx.QueryRowContext(ctx, `
		SELECT username, display_name, is_platform_admin, is_active FROM users WHERE id = $1 FOR UPDATE
	`, id).Scan(&old.Username, &old.DisplayName, &old.IsPlatformAdmin, &old.IsActive)
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, errUserNotFound
	}
	if err != nil {
		return userRecord{}, err
	}

	next := applyPatch(old, patch)
	if err := validateUser(next.Username, next.DisplayName, nil, false); err != nil {
		return userRecord{}, err
	}
	if id == actorID && old.IsPlatformAdmin && !next.IsPlatformAdmin {
		return userRecord{}, errSelfDemote
	}
	if id == actorID && !next.IsActive {
		return userRecord{}, errSelfDeactivate
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE users
		SET username = $2, display_name = $3, is_platform_admin = $4, is_active = $5, updated_at = now()
		WHERE id = $1
	`, id, next.Username, next.DisplayName, next.IsPlatformAdmin, next.IsActive)
	if isDuplicate(err) {
		return userRecord{}, errUsernameTaken
	}
	if err != nil {
		return userRecord{}, err
	}
	if newHash != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, newHash); err != nil {
			return userRecord{}, err
		}
	}

	if err := applySessionEffects(ctx, tx, old, next, newHash != ""); err != nil {
		return userRecord{}, err
	}
	return next, tx.Commit()
}

// gabungin data lama dengan field patch yang dikirim, username dan nama tampilan dirapihin
func applyPatch(old userRecord, patch userPatch) userRecord {
	next := old
	if patch.Username != nil {
		next.Username = auth.NormalizeUsername(*patch.Username)
	}
	if patch.DisplayName != nil {
		next.DisplayName = defaultDisplayName(*patch.DisplayName, next.Username)
	}
	if patch.IsPlatformAdmin != nil {
		next.IsPlatformAdmin = *patch.IsPlatformAdmin
	}
	if patch.IsActive != nil {
		next.IsActive = *patch.IsActive
	}
	return next
}

// session dicabut kalau akses berkurang (password ganti, admin dicabut, dinonaktifkan),
// kalau akses nambah cukup naikin privilege biar session dirotasi
func applySessionEffects(ctx context.Context, tx *sql.Tx, old, next userRecord, passwordChanged bool) error {
	ids := []int64{old.ID}
	lostAdmin := old.IsPlatformAdmin && !next.IsPlatformAdmin
	deactivated := old.IsActive && !next.IsActive
	if passwordChanged || lostAdmin || deactivated {
		return auth.RevokeSessions(ctx, tx, ids)
	}
	if !old.IsPlatformAdmin && next.IsPlatformAdmin {
		return auth.BumpPrivilege(ctx, tx, ids)
	}
	return nil
}

// nama tampilan kosong diisi username, sama kayak M1
func defaultDisplayName(displayName, username string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return username
	}
	return displayName
}

// cek username dan nama tampilan, password ikut dicek kalau dikasih (wajib kalau passwordRequired)
func validateUser(username, displayName string, password *string, passwordRequired bool) error {
	if username == "" || len(username) > 100 {
		return validationError{"username wajib diisi, maksimal 100 karakter"}
	}
	if len(displayName) > 150 {
		return validationError{"nama tampilan maksimal 150 karakter"}
	}
	if password == nil || (*password == "" && !passwordRequired) {
		return nil
	}
	return validatePassword(*password)
}

// password harus 8-72 karakter
func validatePassword(password string) error {
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return validationError{fmt.Sprintf("password harus %d sampai %d karakter", minPasswordLength, maxPasswordLength)}
	}
	return nil
}
