package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

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

// groupAPIID membaca ID positif dari URL dan menjawab ID rusak dengan error JSON.
func groupAPIID(c *gin.Context, name string) (int64, bool) {
	id, err := api.ParseID(c.Param(name))
	if err != nil {
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
	if err := api.Decode(c, &input, false); err != nil {
		return groupChanges{}, err
	}
	var changes groupChanges
	if input.Name != nil {
		var name string
		if err := json.Unmarshal(input.Name, &name); err != nil {
			return groupChanges{}, api.ErrInvalidInput
		}
		changes.Name = &name
	}
	if input.Description != nil {
		changes.SetDescription = true
		if err := json.Unmarshal(input.Description, &changes.Description); err != nil {
			return groupChanges{}, api.ErrInvalidInput
		}
	}
	if input.LeaderUserID != nil {
		changes.SetLeader = true
		var id *string
		if err := json.Unmarshal(input.LeaderUserID, &id); err != nil {
			return groupChanges{}, api.ErrInvalidInput
		}
		if id != nil {
			parsed, err := api.ParseID(*id)
			if err != nil {
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
	if errors.Is(err, api.ErrInvalidInput) {
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
	api.Fail(c, status, code, message, nil)
}

// list mengembalikan satu halaman group aktif yang dapat dikelola admin.
func (h groupAPIHandler) list(c *gin.Context) {
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	groups, err := h.groups.listPage(c.Request.Context(), user, page.Limit+1, page.Offset)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	hasMore := len(groups) > page.Limit
	if hasMore {
		groups = groups[:page.Limit]
	}
	data := make([]groupDTO, 0, len(groups))
	for _, group := range groups {
		data = append(data, groupResponse(group))
	}
	api.List(c, data, page, hasMore)
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
	api.JSON(c, http.StatusCreated, groupResponse(group))
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
	api.JSON(c, http.StatusOK, groupResponse(group))
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
	api.JSON(c, http.StatusOK, groupResponse(group))
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
	api.JSON(c, http.StatusOK, nil)
}
