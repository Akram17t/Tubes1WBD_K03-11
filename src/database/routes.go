package database

import (
	"database/sql"
	"errors"
	"net/http"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, db *sql.DB) {
	router.GET("/databases", auth.RequireLogin(), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		databases, err := GetDatabases(c.Request.Context(), db, user)
		if err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				c.String(http.StatusForbidden, "Forbidden")
			} else {
				c.String(http.StatusInternalServerError, "Terjadi kesalahan internal")
			}
			return
		}
		c.HTML(http.StatusOK, "databases.html", gin.H{"Databases": databases})
	})
	router.GET("/databases/:dbID", notImplemented)
	router.GET("/databases/:dbID/schema", notImplemented)
	router.GET("/databases/:dbID/tables/:table/data", notImplemented)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
