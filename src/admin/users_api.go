package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// userAPIHandler nampung route JSON /api/admin/users, aturan bisnisnya tetap di userService
type userAPIHandler struct {
	users userService
}

// bentuk user yang dikirim ke SPA, password ga pernah ikut
type userDTO struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
	IsActive        bool   `json:"is_active"`
}

// body POST /api/admin/users
type createUserRequest struct {
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	Password        string `json:"password"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

// body PATCH, pointer nil artinya field ga dikirim jadi ga diubah
type patchUserRequest struct {
	Username        *string `json:"username"`
	DisplayName     *string `json:"display_name"`
	Password        *string `json:"password"`
	IsPlatformAdmin *bool   `json:"is_platform_admin"`
	IsActive        *bool   `json:"is_active"`
}

// daftarin route JSON admin user, cuma Platform Admin yang boleh
func registerUserAPIRoutes(r gin.IRouter, users userService) {
	h := userAPIHandler{users: users}
	router := r.Group("/api/admin/users", auth.RequireLogin(), auth.RequireAdmin())
	router.GET("", h.list)
	router.POST("", h.create)
	router.GET("/:user_id", h.get)
	router.PATCH("/:user_id", h.update)
}

// daftar user pakai pagination limit/offset
func (h userAPIHandler) list(c *gin.Context) {
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	users, hasMore, err := h.users.list(c.Request.Context(), page.Limit, page.Offset)
	if err != nil {
		api.Internal(c, err)
		return
	}
	items := make([]userDTO, len(users))
	for i, u := range users {
		items[i] = toUserDTO(u)
	}
	api.List(c, items, page, hasMore)
}

// bikin user baru, balas 201 + data user
func (h userAPIHandler) create(c *gin.Context) {
	var body createUserRequest
	if !api.BindJSON(c, &body) {
		return
	}
	u, err := h.users.create(c.Request.Context(), userInput(body))
	if err != nil {
		failUser(c, err)
		return
	}
	slog.InfoContext(c.Request.Context(), "admin.user_created", "target_user_id", api.FormatID(u.ID), "is_platform_admin", u.IsPlatformAdmin)
	api.JSON(c, http.StatusCreated, toUserDTO(u))
}

// detail satu user
func (h userAPIHandler) get(c *gin.Context) {
	id, ok := api.PathID(c, "user_id")
	if !ok {
		return
	}
	u, err := h.users.get(c.Request.Context(), id)
	if err != nil {
		failUser(c, err)
		return
	}
	api.JSON(c, http.StatusOK, toUserDTO(u))
}

// ubah sebagian field user, termasuk aktif/nonaktif lewat is_active
func (h userAPIHandler) update(c *gin.Context) {
	id, ok := api.PathID(c, "user_id")
	if !ok {
		return
	}
	var body patchUserRequest
	if !api.BindJSON(c, &body) {
		return
	}
	fields := changedFields(body)
	if len(fields) == 0 {
		api.Fail(c, http.StatusBadRequest, api.CodeInvalidInput, "Tidak ada field yang diubah.", nil)
		return
	}

	me, _ := auth.CurrentUser(c)
	u, err := h.users.update(c.Request.Context(), me.ID, id, userPatch(body))
	if err != nil {
		failUser(c, err)
		return
	}
	// yang dicatat cuma nama field-nya, nilai (apalagi password) ga ikut ke log
	slog.InfoContext(c.Request.Context(), "admin.user_updated", "target_user_id", api.FormatID(id), "fields", fields)
	api.JSON(c, http.StatusOK, toUserDTO(u))
}

// terjemahin error service ke response JSON sesuai kontrak
func failUser(c *gin.Context, err error) {
	var invalid validationError
	switch {
	case errors.As(err, &invalid):
		api.Fail(c, http.StatusBadRequest, api.CodeInvalidInput, invalid.msg, nil)
	case errors.Is(err, errUsernameTaken):
		api.Fail(c, http.StatusConflict, api.CodeStateConflict, "Username sudah dipakai.", map[string]any{"field": "username"})
	case errors.Is(err, errSelfDemote), errors.Is(err, errSelfDeactivate):
		api.Fail(c, http.StatusForbidden, api.CodeForbidden, err.Error()+".", nil)
	case errors.Is(err, errUserNotFound):
		api.NotFound(c)
	default:
		api.Internal(c, err)
	}
}

// nama-nama field yang dikirim di PATCH, buat validasi body kosong dan log
func changedFields(body patchUserRequest) []string {
	fields := []string{}
	for name, set := range map[string]bool{
		"username":          body.Username != nil,
		"display_name":      body.DisplayName != nil,
		"password":          body.Password != nil,
		"is_platform_admin": body.IsPlatformAdmin != nil,
		"is_active":         body.IsActive != nil,
	} {
		if set {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}

// ubah record service ke bentuk JSON, ID jadi string
func toUserDTO(u userRecord) userDTO {
	return userDTO{
		ID:              api.FormatID(u.ID),
		Username:        u.Username,
		DisplayName:     u.DisplayName,
		IsPlatformAdmin: u.IsPlatformAdmin,
		IsActive:        u.IsActive,
	}
}
