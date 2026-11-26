package api

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// tangkap panic biar server ga mati. Request /api dapet JSON 500,
// selain /api (asset SPA) dapet 500 polos
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

// route yang ga ketemu: /api/* dapet JSON 404, GET halaman lain dilayani hasil build SPA.
// spaDir kosong / belum di-build -> 404 teks biasa kayak sebelumnya
func NoRoute(spaDir string) gin.HandlerFunc {
	var files fs.FS
	if spaDir != "" {
		if _, err := os.Stat(spaDir + "/index.html"); err == nil {
			files = os.DirFS(spaDir)
		}
	}
	return func(c *gin.Context) {
		if IsAPIPath(c.Request.URL.Path) {
			NotFound(c)
			return
		}
		method := c.Request.Method
		if files == nil || (method != http.MethodGet && method != http.MethodHead) {
			c.String(http.StatusNotFound, "404 page not found")
			return
		}
		serveSPA(c, files)
	}
}

// file build yang beneran ada (js/css/gambar) dikirim apa adanya,
// path lain dapet index.html biar React Router yang nentuin halamannya
func serveSPA(c *gin.Context, files fs.FS) {
	name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
	if info, err := fs.Stat(files, name); err == nil && !info.IsDir() && name != "index.html" {
		http.ServeFileFS(c.Writer, c.Request, files, name)
		return
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		c.String(http.StatusNotFound, "404 page not found")
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", index)
}
