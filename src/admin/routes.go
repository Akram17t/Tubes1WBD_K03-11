package admin

import (
	"database/sql"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes memasang API JSON pengelolaan user, group, dan akses database khusus Platform Admin.
func RegisterRoutes(r gin.IRouter, db *sql.DB) {
	registerGroupAPIRoutes(r, db)
	registerUserAPIRoutes(r, userService{db: db})
}
