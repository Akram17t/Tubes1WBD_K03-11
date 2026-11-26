package database

import (
	"database/sql"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes memasang API viewer database (daftar, detail, schema, tabel, data).
func RegisterRoutes(router gin.IRouter, db *sql.DB, targets TargetProvider) {
	registerViewerAPIRoutes(router, db, targets)
}

type viewerHandler struct {
	db      *sql.DB
	targets TargetProvider
}
