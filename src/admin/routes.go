package admin

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, _ *sql.DB) {
	router.GET("/admin/users", notImplemented)
	router.GET("/admin/users/new", notImplemented)
	router.POST("/admin/users", notImplemented)
	router.GET("/admin/users/:userID/edit", notImplemented)
	router.POST("/admin/users/:userID/update", notImplemented)
	router.POST("/admin/users/:userID/deactivate", notImplemented)
	router.POST("/admin/users/:userID/activate", notImplemented)

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
