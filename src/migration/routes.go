package migration

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router gin.IRoutes, _ *sql.DB) {
	router.GET("/databases/:dbID/migrations", notImplemented)
	router.GET("/databases/:dbID/migrations/new", notImplemented)
	router.POST("/databases/:dbID/migrations", notImplemented)
	router.GET("/databases/:dbID/migrations/:migrationID", notImplemented)
	router.GET("/databases/:dbID/migrations/:migrationID/edit", notImplemented)
	router.POST("/databases/:dbID/migrations/:migrationID/update", notImplemented)
	router.POST("/databases/:dbID/migrations/:migrationID/execute", notImplemented)
	router.GET("/databases/:dbID/migrations/:migrationID/executions/:executionID", notImplemented)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
