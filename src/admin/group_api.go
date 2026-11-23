package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/httpapi"

	"github.com/gin-gonic/gin"
)

type groupAPIHandler struct {
	groups groupService
	access databaseAccessService
}

type groupDTO struct {
	ID             int64    `json:"id,string"`
	Name           string   `json:"name"`
	Description    *string  `json:"description"`
	LeaderUserID   *int64   `json:"leader_user_id,string"`
	IsArchived     bool     `json:"is_archived"`
	AllowedActions []string `json:"allowed_actions"`
}

type groupMemberDTO struct {
	ID          int64  `json:"id,string"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	IsActive    bool   `json:"is_active"`
	IsLeader    bool   `json:"is_leader"`
}

type groupGrantDTO struct {
	GroupID      int64  `json:"group_id,string"`
	DatabaseID   int64  `json:"database_id,string"`
	DatabaseName string `json:"database_name"`
	AccessLevel  string `json:"access_level"`
	IsActive     bool   `json:"is_active"`
}

// registerGroupAPIRoutes memasang API group dengan session admin dan pemeriksaan CSRF.
func registerGroupAPIRoutes(router gin.IRouter, db *sql.DB) {
	h := groupAPIHandler{
		groups: groupService{db: db, store: groupRepository{db: db}},
		access: databaseAccessService{db: db},
	}
	routes := router.Group("/api/admin/groups", auth.RequireLogin(), auth.RequireAdmin(), auth.CSRF())
	routes.GET("", h.list)
	routes.POST("", h.create)
	routes.GET("/:group_id", h.get)
	routes.PATCH("/:group_id", h.patch)
	routes.DELETE("/:group_id", h.archive)
	routes.GET("/:group_id/members", h.members)
	routes.POST("/:group_id/members", h.addMember)
	routes.DELETE("/:group_id/members/:user_id", h.removeMember)
	routes.GET("/:group_id/access", h.grants)
	routes.GET("/:group_id/access/:database_id", h.getGrant)
	routes.PUT("/:group_id/access/:database_id", h.saveGrant)
	routes.DELETE("/:group_id/access/:database_id", h.revokeGrant)
}

// groupResponse mengubah model M1 menjadi DTO dengan ID string dan deskripsi nullable.
func groupResponse(group Group) groupDTO {
	var description *string
	if group.Description != "" {
		description = &group.Description
	}
	return groupDTO{group.ID, group.Name, description, group.LeaderUserID, group.IsArchived, []string{"edit"}}
}

// grantResponse hanya mengirim identitas database dan level akses tanpa credential koneksi.
func grantResponse(groupID int64, grant DatabaseGrant) groupGrantDTO {
	return groupGrantDTO{groupID, grant.DatabaseID, grant.DatabaseName, grant.AccessLevel, grant.IsActive}
}

// groupAPIID membaca ID positif dari URL tanpa memakai format error HTML M1.
func groupAPIID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		groupAPIError(c, errInvalidGroupID)
		return 0, false
	}
	return id, true
}

// readGroupChanges membedakan field yang tidak dikirim dari null yang mengosongkan nilai.
func readGroupChanges(c *gin.Context) (groupChanges, error) {
	var input struct {
		Name         json.RawMessage `json:"name"`
		Description  json.RawMessage `json:"description"`
		LeaderUserID json.RawMessage `json:"leader_user_id"`
	}
	if err := httpapi.Decode(c, &input); err != nil {
		return groupChanges{}, err
	}
	var changes groupChanges
	if input.Name != nil {
		var name string
		if err := json.Unmarshal(input.Name, &name); err != nil {
			return groupChanges{}, httpapi.ErrInvalidInput
		}
		changes.Name = &name
	}
	if input.Description != nil {
		changes.SetDescription = true
		if err := json.Unmarshal(input.Description, &changes.Description); err != nil {
			return groupChanges{}, httpapi.ErrInvalidInput
		}
	}
	if input.LeaderUserID != nil {
		changes.SetLeader = true
		var id *string
		if err := json.Unmarshal(input.LeaderUserID, &id); err != nil {
			return groupChanges{}, httpapi.ErrInvalidInput
		}
		if id != nil {
			parsed, err := strconv.ParseInt(*id, 10, 64)
			if err != nil || parsed <= 0 {
				return groupChanges{}, errInvalidGroupID
			}
			changes.LeaderUserID = &parsed
		}
	}
	return changes, nil
}

// groupAPIError memakai pemetaan error service M1 dengan envelope JSON yang aman.
func groupAPIError(c *gin.Context, err error) {
	status, message := databaseAccessHTTPError(err)
	if errors.Is(err, httpapi.ErrInvalidInput) {
		status, message = http.StatusBadRequest, "Input JSON atau parameter tidak valid."
	}
	code := "internal_error"
	switch status {
	case http.StatusBadRequest:
		code = "invalid_input"
	case http.StatusForbidden:
		code = "forbidden"
	case http.StatusNotFound:
		code = "not_found"
	case http.StatusConflict:
		code = "state_conflict"
	}
	httpapi.Abort(c, status, code, message)
}

// groupAPIList mengirim pagination berdasarkan satu row tambahan yang dibaca dari database.
func groupAPIList(c *gin.Context, data any, limit, offset int, hasMore bool) {
	meta := httpapi.Meta(c)
	meta["limit"], meta["offset"], meta["has_more"] = limit, offset, hasMore
	c.JSON(http.StatusOK, gin.H{"data": data, "meta": meta})
}

// list mengembalikan satu halaman group aktif yang dapat dikelola admin.
func (h groupAPIHandler) list(c *gin.Context) {
	limit, offset, err := httpapi.Pagination(c)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	groups, err := h.groups.listPage(c.Request.Context(), user, limit+1, offset)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	hasMore := len(groups) > limit
	if hasMore {
		groups = groups[:limit]
	}
	data := make([]groupDTO, 0, len(groups))
	for _, group := range groups {
		data = append(data, groupResponse(group))
	}
	groupAPIList(c, data, limit, offset, hasMore)
}

// create membuat group kosong sebelum admin menambahkan anggota dan menetapkan leader.
func (h groupAPIHandler) create(c *gin.Context) {
	changes, err := readGroupChanges(c)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	if changes.Name == nil {
		groupAPIError(c, errInvalidGroupName)
		return
	}
	// Group baru belum memiliki anggota sehingga tidak boleh langsung menunjuk leader.
	if changes.LeaderUserID != nil {
		groupAPIError(c, errInvalidGroupLeader)
		return
	}
	var description string
	if changes.Description != nil {
		description = *changes.Description
	}
	user, _ := auth.CurrentUser(c)
	group, err := h.groups.create(c.Request.Context(), user, *changes.Name, description)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	httpapi.Respond(c, http.StatusCreated, groupResponse(group))
}

// get mengembalikan detail group aktif dengan format DTO API.
func (h groupAPIHandler) get(c *gin.Context) {
	id, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	group, err := h.groups.find(c.Request.Context(), user, id)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	httpapi.Respond(c, http.StatusOK, groupResponse(group))
}

// patch menerapkan perubahan parsial group dan leader dalam satu transaction.
func (h groupAPIHandler) patch(c *gin.Context) {
	id, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	changes, err := readGroupChanges(c)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	group, err := h.groups.patch(c.Request.Context(), user, id, changes)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	httpapi.Respond(c, http.StatusOK, groupResponse(group))
}

// archive mencabut akses dan session anggota sambil mempertahankan record group.
func (h groupAPIHandler) archive(c *gin.Context) {
	id, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.groups.deleteGroup(c.Request.Context(), user, id); err != nil {
		groupAPIError(c, err)
		return
	}
	httpapi.Respond(c, http.StatusOK, nil)
}
