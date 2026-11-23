package admin

import (
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/httpapi"

	"github.com/gin-gonic/gin"
)

// members mengembalikan anggota beserta penanda leader dengan pagination server.
func (h groupAPIHandler) members(c *gin.Context) {
	groupID, ok := groupAPIID(c, "group_id")
	if !ok {
		return
	}
	limit, offset, err := httpapi.Pagination(c)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	group, members, err := h.groups.membersPage(c.Request.Context(), user, groupID, limit+1, offset)
	if err != nil {
		groupAPIError(c, err)
		return
	}
	hasMore := len(members) > limit
	if hasMore {
		members = members[:limit]
	}
	data := make([]groupMemberDTO, 0, len(members))
	for _, member := range members {
		isLeader := group.LeaderUserID != nil && *group.LeaderUserID == member.ID
		data = append(data, groupMemberDTO{member.ID, member.Username, member.DisplayName, member.IsActive, isLeader})
	}
	groupAPIList(c, data, limit, offset, hasMore)
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
	if err := httpapi.Decode(c, &input); err != nil {
		groupAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.groups.addMember(c.Request.Context(), user, groupID, input.UserID); err != nil {
		groupAPIError(c, err)
		return
	}
	httpapi.Respond(c, http.StatusCreated, gin.H{
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
	httpapi.Respond(c, http.StatusOK, nil)
}
