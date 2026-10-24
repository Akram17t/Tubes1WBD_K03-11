package admin

import (
	"database/sql"
	"net/http"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// semua route /admin cuma buat Platform Admin
func RegisterRoutes(r gin.IRouter, db *sql.DB) {
	router := r.Group("", auth.RequireLogin(), auth.RequireAdmin())

	users := &userHandler{db: db}
	router.GET("/admin/users", users.list)
	router.GET("/admin/users/new", users.newForm)
	router.POST("/admin/users", users.create)
	router.GET("/admin/users/:userID/edit", users.edit)
	router.POST("/admin/users/:userID/update", users.update)
	router.POST("/admin/users/:userID/deactivate", users.deactivate)
	router.POST("/admin/users/:userID/activate", users.activate)

	router.GET("/admin/groups", notImplemented)
	router.GET("/admin/groups/new", notImplemented)
	router.POST("/admin/groups", notImplemented)
	router.GET("/admin/groups/:groupID/edit", notImplemented)
	router.POST("/admin/groups/:groupID/update", notImplemented)
	router.POST("/admin/groups/:groupID/delete", notImplemented)
	router.POST("/admin/groups/:groupID/members", notImplemented)
	router.POST("/admin/groups/:groupID/members/:userID/remove", notImplemented)

	router.GET("/admin/databases", notImplemented)
	router.GET("/admin/databases/new", notImplemented)
	router.POST("/admin/databases", notImplemented)
	router.GET("/admin/databases/:dbID/edit", notImplemented)
	router.POST("/admin/databases/:dbID/update", notImplemented)
	router.POST("/admin/databases/:dbID/test", notImplemented)
	router.POST("/admin/databases/:dbID/deactivate", notImplemented)
	router.POST("/admin/databases/:dbID/activate", notImplemented)

	router.GET("/admin/groups/:groupID/database-access", notImplemented)
	router.POST("/admin/groups/:groupID/database-access", notImplemented)
	router.POST("/admin/groups/:groupID/database-access/:dbID/revoke", notImplemented)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
