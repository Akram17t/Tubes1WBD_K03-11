package auth

import (
	"database/sql"

	"github.com/gin-gonic/gin"
)

// pasang middleware session + CSRF ke semua route, lalu daftarin /api/auth.
// Harus dipanggil sebelum package lain daftarin route-nya.
func RegisterRoutes(router gin.IRouter, db *sql.DB, config SessionConfig) {
	h := &Handler{DB: db, Config: config}
	router.Use(h.Session(), CSRF())
	registerAPIRoutes(router, h)
}
