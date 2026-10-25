package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

type databaseAccessHandler struct{ service databaseAccessService }

func registerDatabaseAccessRoutes(router gin.IRouter, db *sql.DB) {
	routes := router.Group("/admin/groups/:groupID/database-access",
		auth.RequireLogin(), auth.RequireAdmin(), parseDatabaseAccessForm, auth.CSRF())
	handler := databaseAccessHandler{service: databaseAccessService{db: db}}
	routes.GET("", handler.show)
	routes.POST("", handler.save)
	routes.POST("/:dbID/revoke", handler.revoke)
}

func parseDatabaseAccessForm(c *gin.Context) {
	if c.Request.Method == http.MethodPost {
		if err := c.Request.ParseForm(); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
	}
	c.Next()
}

func (h databaseAccessHandler) show(c *gin.Context) {
	if groupID, ok := groupIDParam(c, "groupID"); ok {
		h.render(c, groupID, http.StatusOK, "", 0, "view")
	}
}

func (h databaseAccessHandler) save(c *gin.Context) {
	groupID, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	databaseID, err := strconv.ParseInt(c.PostForm("database_id"), 10, 64)
	if err != nil || databaseID <= 0 {
		c.String(http.StatusBadRequest, "%s", errInvalidDatabaseID)
		return
	}
	level := c.PostForm("access_level")
	user, _ := auth.CurrentUser(c)
	if err := h.service.save(c.Request.Context(), user, groupID, databaseID, level); err != nil {
		status, message := databaseAccessHTTPError(err)
		if status == http.StatusBadRequest || status == http.StatusConflict {
			h.render(c, groupID, status, message, databaseID, level)
		} else {
			c.String(status, "%s", message)
		}
		return
	}
	c.Redirect(http.StatusSeeOther, databaseAccessURL(groupID))
}

func (h databaseAccessHandler) revoke(c *gin.Context) {
	groupID, ok := groupIDParam(c, "groupID")
	if !ok {
		return
	}
	databaseID, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
	if err != nil || databaseID <= 0 {
		c.String(http.StatusBadRequest, "%s", errInvalidDatabaseID)
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.revoke(c.Request.Context(), user, groupID, databaseID); err != nil {
		status, message := databaseAccessHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	c.Redirect(http.StatusSeeOther, databaseAccessURL(groupID))
}

func (h databaseAccessHandler) render(c *gin.Context, groupID int64, status int, message string, selectedID int64, selectedLevel string) {
	user, _ := auth.CurrentUser(c)
	page, err := h.service.page(c.Request.Context(), user, groupID)
	if err != nil {
		status, message := databaseAccessHTTPError(err)
		c.String(status, "%s", message)
		return
	}
	c.HTML(status, "database_access.html", gin.H{
		"Group": page.Group, "Grants": page.Grants, "Databases": page.Databases,
		"SelectedDatabaseID": selectedID, "SelectedLevel": selectedLevel,
		"Error": message, "CSRFToken": auth.CSRFToken(c), "User": user,
	})
}

func databaseAccessURL(groupID int64) string {
	return "/admin/groups/" + strconv.FormatInt(groupID, 10) + "/database-access"
}

func databaseAccessHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, errInvalidDatabaseID), errors.Is(err, errInvalidAccessLevel):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errAccessDatabaseNotFound), errors.Is(err, errDatabaseAccessNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, errAccessDatabaseInactive):
		return http.StatusConflict, err.Error()
	default:
		return groupHTTPError(err)
	}
}
