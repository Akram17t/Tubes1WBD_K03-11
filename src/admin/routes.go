package admin

import (
	"database/sql"
	"net/http"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes memasang halaman M1 dan API pengelolaan group khusus Platform Admin.
func RegisterRoutes(r gin.IRouter, db *sql.DB) {
	registerGroupAPIRoutes(r, db)
	router := r.Group("", auth.RequireLogin(), auth.RequireAdmin())

	userSvc := userService{db: db}
	users := &userHandler{users: userSvc}
	router.GET("/admin/users", users.list)
	router.GET("/admin/users/new", users.newForm)
	router.POST("/admin/users", users.create)
	router.GET("/admin/users/:userID/edit", users.edit)
	router.POST("/admin/users/:userID/update", users.update)
	router.POST("/admin/users/:userID/deactivate", users.deactivate)
	router.POST("/admin/users/:userID/activate", users.activate)

	registerGroupRoutes(router, groupService{db: db, store: groupRepository{db: db}})

	registerDatabaseAccessRoutes(router, db)

	// route JSON buat SPA, halaman HTML di atas tetap hidup sampai SPA-nya jadi
	registerUserAPIRoutes(r, userSvc)
}

// notImplemented mengembalikan status untuk fitur HTML yang belum tersedia.
func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
