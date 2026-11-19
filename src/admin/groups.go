package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

type Group struct {
	ID           int64
	Name         string
	Description  string
	LeaderUserID *int64
	IsArchived   bool
}

type GroupMember struct {
	ID          int64
	Username    string
	DisplayName string
	IsActive    bool
}

type groupEditData struct {
	Group          Group
	Members        []GroupMember
	AvailableUsers []GroupMember
}

var (
	errInvalidGroupName    = errors.New("Nama group wajib diisi dan maksimal 100 karakter.")
	errGroupNameExists     = errors.New("Nama group sudah digunakan.")
	errInvalidGroupID      = errors.New("ID group atau user tidak valid.")
	errGroupNotFound       = errors.New("Group tidak ditemukan.")
	errGroupUserNotFound   = errors.New("User tidak ditemukan.")
	errGroupMemberNotFound = errors.New("User bukan anggota group ini.")
	errGroupMemberExists   = errors.New("User sudah menjadi anggota group ini.")
	errInvalidGroupLeader  = errors.New("Leader harus anggota group dengan akun aktif.")
)

type groupActions interface {
	list(context.Context, *auth.User) ([]Group, error)
	create(context.Context, *auth.User, string, string) error
	edit(context.Context, *auth.User, int64) (groupEditData, error)
	update(context.Context, *auth.User, int64, string, string) error
	deleteGroup(context.Context, *auth.User, int64) error
	addMember(context.Context, *auth.User, int64, int64) error
	removeMember(context.Context, *auth.User, int64, int64) error
	setLeader(context.Context, *auth.User, int64, *int64) error
}

// Handlers: map HTTP requests to the service and templates.
type groupHandler struct{ service groupActions }

// registerGroupRoutes memasang endpoint pengelolaan group khusus admin.
func registerGroupRoutes(router gin.IRouter, service groupActions) {
	routes := router.Group("/admin/groups", auth.RequireLogin(), auth.RequireAdmin())
	handler := groupHandler{service: service}
	routes.GET("", handler.list)
	routes.GET("/new", handler.new)
	routes.POST("", handler.create)
	routes.GET("/:groupID/edit", handler.edit)
	routes.POST("/:groupID/update", handler.update)
	routes.POST("/:groupID/leader", handler.setLeader)
	routes.POST("/:groupID/delete", handler.deleteGroup)
	routes.POST("/:groupID/members", handler.addMember)
	routes.POST("/:groupID/members/:userID/remove", handler.removeMember)
}

// list menampilkan daftar group aktif dan token CSRF untuk admin.
func (h groupHandler) list(c *gin.Context) {
	user, _ := auth.CurrentUser(c)
	groups, err := h.service.list(c.Request.Context(), user)
	if err != nil {
		status, message := groupHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	c.HTML(http.StatusOK, "groups.html", gin.H{"Groups": groups, "User": user, "CSRFToken": auth.CSRFToken(c)})
}

// new menampilkan form pembuatan group kosong.
func (h groupHandler) new(c *gin.Context) {
	h.form(c, http.StatusOK, Group{}, "")
}

// create menyimpan input form atau menampilkan kembali kesalahan validasinya.
func (h groupHandler) create(c *gin.Context) {
	group := Group{
		Name:        c.Request.PostForm.Get("name"),
		Description: c.Request.PostForm.Get("description"),
	}
	user, _ := auth.CurrentUser(c)
	err := h.service.create(c.Request.Context(), user, group.Name, group.Description)
	if err != nil {
		status, message := groupHTTPError(err)
		if status == http.StatusBadRequest || status == http.StatusConflict {
			h.form(c, status, group, message)
		} else {
			c.String(status, "%s", message)
		}
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/groups")
}

// edit membaca ID group sebelum menampilkan halaman pengelolaan.
func (h groupHandler) edit(c *gin.Context) {
	id, ok := groupIDParam(c, "groupID")
	if ok {
		h.editForm(c, id, http.StatusOK, "", nil)
	}
}

// update menyimpan perubahan nama dan deskripsi dari form admin.
func (h groupHandler) update(c *gin.Context) {
	id, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	group := Group{ID: id, Name: c.PostForm("name"), Description: c.PostForm("description")}
	user, _ := auth.CurrentUser(c)
	if err := h.service.update(c.Request.Context(), user, id, group.Name, group.Description); err != nil {
		status, message := groupHTTPError(err)
		if status == http.StatusBadRequest || status == http.StatusConflict {
			h.editForm(c, id, status, message, &group)
		} else {
			c.String(status, "%s", message)
		}
		return
	}
	c.Redirect(http.StatusSeeOther, groupEditURL(id))
}

// deleteGroup mengarsipkan group lalu mengarahkan admin kembali ke daftar aktif.
func (h groupHandler) deleteGroup(c *gin.Context) {
	id, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.deleteGroup(c.Request.Context(), user, id); err != nil {
		status, message := groupHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/groups")
}

// setLeader membaca pilihan leader dan nilai kosong berarti mengosongkan leader.
func (h groupHandler) setLeader(c *gin.Context) {
	id, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	value, present := c.GetPostForm("leader_user_id")
	if !present {
		c.String(http.StatusBadRequest, "%s", errInvalidGroupID)
		return
	}
	// Nilai kosong sengaja dibedakan dari field yang tidak dikirim.
	var leaderID *int64
	if value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			c.String(http.StatusBadRequest, "%s", errInvalidGroupID)
			return
		}
		leaderID = &parsed
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.setLeader(c.Request.Context(), user, id, leaderID); err != nil {
		status, message := groupHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	c.Redirect(http.StatusSeeOther, groupEditURL(id))
}

// addMember memvalidasi user dari form sebelum menambahkannya ke group.
func (h groupHandler) addMember(c *gin.Context) {
	id, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(c.PostForm("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		c.String(http.StatusBadRequest, "%s", errInvalidGroupID)
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.addMember(c.Request.Context(), user, id, userID); err != nil {
		status, message := groupHTTPError(err)
		if status == http.StatusConflict {
			h.editForm(c, id, status, message, nil)
		} else {
			c.String(status, "%s", message)
		}
		return
	}
	c.Redirect(http.StatusSeeOther, groupEditURL(id))
}

// removeMember memvalidasi ID group dan user sebelum mencabut keanggotaan.
func (h groupHandler) removeMember(c *gin.Context) {
	id, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	userID, ok := groupIDParam(c, "userID")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.removeMember(c.Request.Context(), user, id, userID); err != nil {
		status, message := groupHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	c.Redirect(http.StatusSeeOther, groupEditURL(id))
}

// editForm merender data pengelolaan sambil mempertahankan input form yang ditolak.
func (h groupHandler) editForm(c *gin.Context, id int64, status int, message string, submitted *Group) {
	user, _ := auth.CurrentUser(c)
	data, err := h.service.edit(c.Request.Context(), user, id)
	if err != nil {
		status, message := groupHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	if submitted != nil {
		submitted.LeaderUserID = data.Group.LeaderUserID
		data.Group = *submitted
	}
	var leaderID int64
	if data.Group.LeaderUserID != nil {
		leaderID = *data.Group.LeaderUserID
	}
	c.HTML(status, "group_edit.html", gin.H{
		"Group": data.Group, "Members": data.Members, "AvailableUsers": data.AvailableUsers,
		"LeaderUserID": leaderID,
		"Error":        message, "CSRFToken": auth.CSRFToken(c), "User": user,
	})
}

// groupIDParam membaca parameter ID positif atau mengirim respons 400.
func groupIDParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		c.String(http.StatusBadRequest, "%s", errInvalidGroupID)
		return 0, false
	}
	return id, true
}

// groupEditURL menyusun alamat halaman pengelolaan satu group.
func groupEditURL(id int64) string {
	return "/admin/groups/" + strconv.FormatInt(id, 10) + "/edit"
}

// form merender form group baru beserta input, error, dan token CSRF-nya.
func (h groupHandler) form(c *gin.Context, status int, group Group, message string) {
	user, _ := auth.CurrentUser(c)
	c.HTML(status, "group_new.html", gin.H{
		"Group": group, "Error": message, "CSRFToken": auth.CSRFToken(c), "User": user,
	})
}

// groupHTTPError memetakan error service ke status HTTP dan pesan aman.
func groupHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "Akses ditolak."
	case errors.Is(err, errInvalidGroupName), errors.Is(err, errInvalidGroupID), errors.Is(err, errInvalidGroupLeader):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errGroupNameExists), errors.Is(err, errGroupMemberExists):
		return http.StatusConflict, err.Error()
	case errors.Is(err, errGroupNotFound), errors.Is(err, errGroupUserNotFound), errors.Is(err, errGroupMemberNotFound):
		return http.StatusNotFound, err.Error()
	default:
		return http.StatusInternalServerError, "Terjadi kesalahan saat mengakses group."
	}
}
