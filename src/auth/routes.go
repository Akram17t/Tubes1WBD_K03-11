package auth

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes owns authentication routes until their handlers are implemented.
func RegisterRoutes(router gin.IRoutes, _ *sql.DB) {
	router.GET("/", notImplemented)
	router.GET("/login", notImplemented)
	router.POST("/login", notImplemented)
	router.POST("/logout", notImplemented)
}

func notImplemented(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not Implemented")
}
