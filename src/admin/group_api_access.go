package admin

import (
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// grants membaca satu halaman grant milik group tanpa memuat credential koneksi.
func (h groupAPIHandler) grants(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	grants, err := h.access.listPage(c.Request.Context(), user, groupID, page.Limit+1, page.Offset)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	hasMore := len(grants) > page.Limit
	if hasMore {
		grants = grants[:page.Limit]
	}
	data := make([]groupGrantDTO, 0, len(grants))
	for _, grant := range grants {
		data = append(data, grantResponse(groupID, grant))
	}
	api.List(c, data, page, hasMore)
}

// getGrant membaca grant berdasarkan pasangan ID group dan database yang diminta.
func (h groupAPIHandler) getGrant(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	databaseID, ok := groupAPIID(c, "database_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	grant, err := h.access.get(c.Request.Context(), user, groupID, databaseID)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	api.JSON(c, http.StatusOK, grantResponse(groupID, grant))
}

// saveGrant menerapkan PUT idempotent sambil menjaga aturan privilege dan session M1.
func (h groupAPIHandler) saveGrant(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	databaseID, ok := groupAPIID(c, "database_id")
	if !ok {
		return
	}
	var input struct {
		AccessLevel string `json:"access_level"`
	}
	if err := api.Decode(c, &input, false); err != nil {
		groupAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	grant, err := h.access.save(c.Request.Context(), user, groupID, databaseID, input.AccessLevel)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	api.JSON(c, http.StatusOK, grantResponse(groupID, grant))
}

// revokeGrant mencabut satu grant dan menutup session anggota yang terdampak.
func (h groupAPIHandler) revokeGrant(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	databaseID, ok := groupAPIID(c, "database_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.access.revoke(c.Request.Context(), user, groupID, databaseID); err != nil {
		groupAPIError(c, err)
		return
	}
	api.JSON(c, http.StatusOK, nil)
}
