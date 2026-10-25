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

	registerGroupRoutes(router, groupService{db: db, store: groupRepository{db: db}})

	registerDatabaseAccessRoutes(router, db)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
