package admin

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRouter, db *sql.DB) {
	router.GET("/admin/users", notImplemented)
	router.GET("/admin/users/new", notImplemented)
	router.POST("/admin/users", notImplemented)
	router.GET("/admin/users/:userID/edit", notImplemented)
	router.POST("/admin/users/:userID/update", notImplemented)
	router.POST("/admin/users/:userID/deactivate", notImplemented)
	router.POST("/admin/users/:userID/activate", notImplemented)

	registerGroupRoutes(router, groupService{db: db, store: groupRepository{db: db}})

	router.GET("/admin/databases", notImplemented)
	router.GET("/admin/databases/new", notImplemented)
	router.POST("/admin/databases", notImplemented)
	router.GET("/admin/databases/:dbID/edit", notImplemented)
	router.POST("/admin/databases/:dbID/update", notImplemented)
	router.POST("/admin/databases/:dbID/test", notImplemented)
	router.POST("/admin/databases/:dbID/deactivate", notImplemented)
	router.POST("/admin/databases/:dbID/activate", notImplemented)

	registerDatabaseAccessRoutes(router, db)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
