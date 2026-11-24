package admin

import (
	"net/http"
	"strconv"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// members mengembalikan anggota beserta penanda leader dengan pagination server.
func (h groupAPIHandler) members(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	group, members, err := h.groups.membersPage(c.Request.Context(), user, groupID, page.Limit+1, page.Offset)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	hasMore := len(members) > page.Limit
	if hasMore {
		members = members[:page.Limit]
	}
	data := make([]groupMemberDTO, 0, len(members))
	for _, member := range members {
		isLeader := group.LeaderUserID != nil && *group.LeaderUserID == member.ID
		data = append(data, groupMemberDTO{member.ID, member.Username, member.DisplayName, member.IsActive, isLeader})
	}
	api.List(c, data, page, hasMore)
}

// addMember menambah anggota dari user_id dan mempertahankan rotasi session M1.
func (h groupAPIHandler) addMember(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	var input struct {
		UserID int64 `json:"user_id,string"`
	}
	if err := api.Decode(c, &input, false); err != nil {
		groupAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.groups.addMember(c.Request.Context(), user, groupID, input.UserID); err != nil {
		groupAPIError(c, err)
		return
	}
	api.JSON(c, http.StatusCreated, gin.H{
		"group_id": strconv.FormatInt(groupID, 10), "user_id": strconv.FormatInt(input.UserID, 10),
	})
}

// removeMember mencabut keanggotaan, jabatan leader terkait, dan session user.
func (h groupAPIHandler) removeMember(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	userID, ok := groupAPIID(c, "user_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.groups.removeMember(c.Request.Context(), user, groupID, userID); err != nil {
		groupAPIError(c, err)
		return
	}
	api.JSON(c, http.StatusOK, nil)
}
