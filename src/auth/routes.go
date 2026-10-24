package auth

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// pasang middleware session + CSRF ke semua route, lalu daftarin route login dan logout.
// Harus dipanggil sebelum package lain daftarin route-nya.
func RegisterRoutes(router gin.IRouter, db *sql.DB, config SessionConfig) {
	h := &Handler{DB: db, Config: config}
	router.Use(h.Session(), CSRF())

	router.GET("/", h.home)
	router.GET("/login", h.showLogin)
	router.POST("/login", h.login)
	router.POST("/logout", RequireLogin(), h.logout)
}

// arahin ke daftar database kalau sudah login, kalau belum ke halaman login
func (h *Handler) home(c *gin.Context) {
	if _, ok := CurrentUser(c); ok {
		c.Redirect(http.StatusSeeOther, "/databases")
		return
	}
	c.Redirect(http.StatusSeeOther, "/login")
}
