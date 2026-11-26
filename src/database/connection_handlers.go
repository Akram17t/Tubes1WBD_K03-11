package database

import "github.com/gin-gonic/gin"

type connectionHandler struct{ service *ConnectionService }

// RegisterAdminRoutes mounts connection administration separately from viewer routes.
func RegisterAdminRoutes(router gin.IRouter, service *ConnectionService) {
	registerConnectionAPIRoutes(router, service)
}
