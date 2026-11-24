package admin

import (
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

// userHandler nampung route HTML administrasi user (halaman lama M1)
type userHandler struct {
	users userService
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
	users, _, err := h.users.list(c.Request.Context(), -1, 0)
	if err != nil {
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
	_, err := h.users.create(c.Request.Context(), userInput(form))
	if err != nil {
		h.renderFormError(c, 0, form, err)
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
	u, err := h.users.get(c.Request.Context(), userID)
	if err != nil {
		h.renderFormError(c, userID, userForm{}, err)
		return
	}
	renderUserForm(c, http.StatusOK, userID, userForm{Username: u.Username, DisplayName: u.DisplayName, IsPlatformAdmin: u.IsPlatformAdmin}, "")
}

// simpan perubahan dari form edit, password kosong artinya ga diganti
func (h *userHandler) update(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	form := readUserForm(c)
	patch := userPatch{Username: &form.Username, DisplayName: &form.DisplayName, IsPlatformAdmin: &form.IsPlatformAdmin}
	if form.Password != "" {
		patch.Password = &form.Password
	}
	if !h.applyPatch(c, userID, patch, form) {
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users")
}

// nonaktifin akun, session-nya ikut dicabut di service
func (h *userHandler) deactivate(c *gin.Context) {
	h.setActive(c, false)
}

// aktifin lagi akun, user-nya tinggal login ulang
func (h *userHandler) activate(c *gin.Context) {
	h.setActive(c, true)
}

// dipakai deactivate/activate, cuma ngubah is_active
func (h *userHandler) setActive(c *gin.Context, active bool) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	if !h.applyPatch(c, userID, userPatch{IsActive: &active}, userForm{}) {
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users")
}

// panggil service update pakai user yang login sebagai actor, false kalau gagal (response sudah dikirim)
func (h *userHandler) applyPatch(c *gin.Context, userID int64, patch userPatch, form userForm) bool {
	me, _ := auth.CurrentUser(c)
	if _, err := h.users.update(c.Request.Context(), me.ID, userID, patch); err != nil {
		h.renderFormError(c, userID, form, err)
		return false
	}
	return true
}

// terjemahin error service ke response HTML, perilakunya sama kayak M1
func (h *userHandler) renderFormError(c *gin.Context, userID int64, form userForm, err error) {
	var invalid validationError
	switch {
	case errors.As(err, &invalid):
		renderUserForm(c, http.StatusBadRequest, userID, form, invalid.msg)
	case errors.Is(err, errUsernameTaken):
		renderUserForm(c, http.StatusConflict, userID, form, err.Error())
	case errors.Is(err, errSelfDemote):
		renderUserForm(c, http.StatusBadRequest, userID, form, err.Error())
	case errors.Is(err, errSelfDeactivate):
		c.String(http.StatusBadRequest, err.Error())
	case errors.Is(err, errUserNotFound):
		c.AbortWithStatus(http.StatusNotFound)
	default:
		c.AbortWithStatus(http.StatusInternalServerError)
	}
}

// ambil isi form dari request, username langsung dirapihin
func readUserForm(c *gin.Context) userForm {
	username := auth.NormalizeUsername(c.PostForm("username"))
	return userForm{
		Username:        username,
		DisplayName:     defaultDisplayName(c.PostForm("display_name"), username),
		Password:        c.PostForm("password"),
		IsPlatformAdmin: c.PostForm("is_platform_admin") == "on",
	}
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
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
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
		"Error":     strings.TrimSpace(errorMessage),
	})
}
