package admin

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	minPasswordLength = 8
	maxPasswordLength = 72 // batas wajar biar hashing ga dipakai buat bikin server berat
)

// userHandler nampung route administrasi user
type userHandler struct {
	db *sql.DB
}

// satu baris user buat tabel daftar user
type userRow struct {
	ID              int64
	Username        string
	DisplayName     string
	IsPlatformAdmin bool
	IsActive        bool
}

// isi form buat bikin atau ngubah user
type userForm struct {
	Username        string
	DisplayName     string
	Password        string
	IsPlatformAdmin bool
}

// tampilin semua user
func (h *userHandler) list(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id, username, display_name, is_platform_admin, is_active
		FROM users ORDER BY username
	`)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsPlatformAdmin, &u.IsActive); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		users = append(users, u)
	}
	if rows.Err() != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	me, _ := auth.CurrentUser(c)
	c.HTML(http.StatusOK, "admin_users.html", gin.H{
		"User":      me,
		"CSRFToken": auth.CSRFToken(c),
		"Users":     users,
	})
}

// tampilin form kosong buat user baru
func (h *userHandler) newForm(c *gin.Context) {
	renderUserForm(c, http.StatusOK, 0, userForm{}, "")
}

// bikin user baru, username dobel dapat 409
func (h *userHandler) create(c *gin.Context) {
	form := readUserForm(c)
	if msg := validateUserForm(form, true); msg != "" {
		renderUserForm(c, http.StatusBadRequest, 0, form, msg)
		return
	}

	hash, err := auth.HashPassword(form.Password)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	_, err = h.db.ExecContext(c.Request.Context(), `
		INSERT INTO users (username, display_name, password_hash, is_platform_admin)
		VALUES ($1, $2, $3, $4)
	`, form.Username, form.DisplayName, hash, form.IsPlatformAdmin)
	if isDuplicate(err) {
		renderUserForm(c, http.StatusConflict, 0, form, "username sudah dipakai")
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users")
}

// tampilin form edit yang sudah keisi data user
func (h *userHandler) edit(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}

	var form userForm
	err := h.db.QueryRowContext(c.Request.Context(), `
		SELECT username, display_name, is_platform_admin FROM users WHERE id = $1
	`, userID).Scan(&form.Username, &form.DisplayName, &form.IsPlatformAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderUserForm(c, http.StatusOK, userID, form, "")
}

// simpan perubahan user, sekalian atur session-nya:
// ganti password atau dicabut adminnya -> session dicabut,
// dijadiin admin -> privilege_version naik biar session dirotasi
func (h *userHandler) update(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	form := readUserForm(c)
	if msg := validateUserForm(form, false); msg != "" {
		renderUserForm(c, http.StatusBadRequest, userID, form, msg)
		return
	}
	me, _ := auth.CurrentUser(c)
	if userID == me.ID && !form.IsPlatformAdmin {
		renderUserForm(c, http.StatusBadRequest, userID, form, "tidak bisa mencabut status admin milik sendiri")
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// kunci baris user biar ga ada admin lain yang ngubah barengan
	var wasAdmin bool
	err = tx.QueryRowContext(ctx, `SELECT is_platform_admin FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&wasAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE users
		SET username = $2, display_name = $3, is_platform_admin = $4, updated_at = now()
		WHERE id = $1
	`, userID, form.Username, form.DisplayName, form.IsPlatformAdmin)
	if isDuplicate(err) {
		renderUserForm(c, http.StatusConflict, userID, form, "username sudah dipakai")
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	passwordChanged := form.Password != ""
	if passwordChanged {
		hash, err := auth.HashPassword(form.Password)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	ids := []int64{userID}
	if passwordChanged || (wasAdmin && !form.IsPlatformAdmin) {
		err = auth.RevokeSessions(ctx, tx, ids)
	} else if !wasAdmin && form.IsPlatformAdmin {
		err = auth.BumpPrivilege(ctx, tx, ids)
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users")
}

// nonaktifin akun dan cabut semua session-nya
func (h *userHandler) deactivate(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	me, _ := auth.CurrentUser(c)
	if userID == me.ID {
		c.String(http.StatusBadRequest, "tidak bisa menonaktifkan akun sendiri")
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE users SET is_active = false, updated_at = now() WHERE id = $1`, userID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if err := auth.RevokeSessions(ctx, tx, []int64{userID}); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users")
}

// aktifin lagi akun, user-nya tinggal login ulang
func (h *userHandler) activate(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}

	result, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE users SET is_active = true, updated_at = now() WHERE id = $1
	`, userID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users")
}

// ambil isi form dari request, username langsung dirapihin
func readUserForm(c *gin.Context) userForm {
	form := userForm{
		Username:        auth.NormalizeUsername(c.PostForm("username")),
		DisplayName:     strings.TrimSpace(c.PostForm("display_name")),
		Password:        c.PostForm("password"),
		IsPlatformAdmin: c.PostForm("is_platform_admin") == "on",
	}
	if form.DisplayName == "" {
		form.DisplayName = form.Username
	}
	return form
}

// cek isi form, balikin pesan error atau "" kalau aman.
// passwordRequired true waktu bikin user baru
func validateUserForm(form userForm, passwordRequired bool) string {
	if form.Username == "" || len(form.Username) > 100 {
		return "username wajib diisi, maksimal 100 karakter"
	}
	if len(form.DisplayName) > 150 {
		return "nama tampilan maksimal 150 karakter"
	}
	if form.Password == "" && !passwordRequired {
		return ""
	}
	if len(form.Password) < minPasswordLength || len(form.Password) > maxPasswordLength {
		return fmt.Sprintf("password harus %d sampai %d karakter", minPasswordLength, maxPasswordLength)
	}
	return ""
}

// ambil :userID dari URL, langsung 404 kalau bukan angka
func userIDParam(c *gin.Context) (int64, bool) {
	userID, err := strconv.ParseInt(c.Param("userID"), 10, 64)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return 0, false
	}
	return userID, true
}

// cek error dari postgres karena nabrak UNIQUE (kode 23505)
func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// render form user, userID 0 artinya form buat user baru
func renderUserForm(c *gin.Context, status int, userID int64, form userForm, errorMessage string) {
	action := "/admin/users"
	if userID != 0 {
		action = fmt.Sprintf("/admin/users/%d/update", userID)
	}

	me, _ := auth.CurrentUser(c)
	c.HTML(status, "admin_user_form.html", gin.H{
		"User":      me,
		"CSRFToken": auth.CSRFToken(c),
		"IsNew":     userID == 0,
		"Action":    action,
		"Form":      form,
		"Error":     errorMessage,
	})
}
