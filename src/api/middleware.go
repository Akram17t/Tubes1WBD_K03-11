package api

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// tangkap panic biar server ga mati. Request /api dapet JSON 500,
// halaman lama (non-API) tetap dapet 500 polos kayak di M1
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		// isi panic sengaja ga di-log, bisa aja berisi data sensitif
		slog.ErrorContext(c.Request.Context(), "http.panic",
			"method", c.Request.Method, "route", routeOf(c))
		if IsAPIPath(c.Request.URL.Path) {
			Fail(c, http.StatusInternalServerError, CodeInternal, internalErrorMessage, nil)
			return
		}
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}

// handler buat route yang ga ketemu: /api/* dapet JSON 404,
// sisanya 404 teks biasa (nanti diganti fallback index.html SPA)
func NoRoute() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsAPIPath(c.Request.URL.Path) {
			NotFound(c)
			return
		}
		c.String(http.StatusNotFound, "404 page not found")
	}
}
