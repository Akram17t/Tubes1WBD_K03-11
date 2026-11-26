package migration

import (
	"database/sql"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes memasang API history M2 menggunakan model execution bersama.
func RegisterRoutes(router gin.IRoutes, db *sql.DB, targets database.TargetProvider) {
	h := handlers{db: db, targets: targets}
	login := auth.RequireLogin()
	router.GET("/api/databases/:database_id/history", login, h.history)
	router.GET("/api/databases/:database_id/executions/:execution_id", login, h.executionDetail)
}

type handlers struct {
	db      *sql.DB
	targets database.TargetProvider
}
