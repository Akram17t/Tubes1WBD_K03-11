package database

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, _ *sql.DB) {
	router.GET("/databases", notImplemented)
	router.GET("/databases/:dbID", notImplemented)
	router.GET("/databases/:dbID/schema", notImplemented)
	router.GET("/databases/:dbID/tables/:table/data", notImplemented)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
