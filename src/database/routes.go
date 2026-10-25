package database

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, db *sql.DB) {
	router.GET("/databases", auth.RequireLogin(), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		databases, err := GetDatabases(c.Request.Context(), db, user)
		if err != nil {
			respondError(c, err)
			return
		}
		c.HTML(http.StatusOK, "databases.html", gin.H{"Databases": databases})
	})
	router.GET("/databases/:dbID", auth.RequireLogin(), func(c *gin.Context) {
		dbID, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
		if err != nil || dbID < 1 {
			respondError(c, ErrInvalidDatabaseID)
			return
		}
		user, _ := auth.CurrentUser(c)
		database, err := GetDatabase(c.Request.Context(), db, user, dbID)
		if err != nil {
			respondError(c, err)
			return
		}
		c.HTML(http.StatusOK, "databases.html", gin.H{"Database": database})
	})
	router.GET("/databases/:dbID/schema", notImplemented)
	router.GET("/databases/:dbID/tables/:table/data", notImplemented)
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidDatabaseID):
		c.String(http.StatusBadRequest, "%s", ErrInvalidDatabaseID)
	case errors.Is(err, auth.ErrForbidden):
		c.String(http.StatusForbidden, "Forbidden")
	case errors.Is(err, ErrDatabaseNotFound):
		c.String(http.StatusNotFound, "%s", ErrDatabaseNotFound)
	default:
		c.String(http.StatusInternalServerError, "Terjadi kesalahan internal")
	}
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
