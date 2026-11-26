package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"tubes-wbd/src/api"
)

// bikin folder build palsu: index.html + satu file asset
func newSPARouter(t *testing.T) *gin.Engine {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=root></div>"), 0o644)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.SPA(dir))
	r.NoRoute(api.NoRoute(dir))
	return r
}

// TestSPAOverridesLegacyGETRoutes memastikan login dan halaman lama membuka SPA setelah build.
func TestSPAOverridesLegacyGETRoutes(t *testing.T) {
	r := newSPARouter(t)
	legacyCalls := 0
	for _, target := range []string{"/", "/login", "/databases", "/admin/groups"} {
		r.GET(target, func(c *gin.Context) {
			legacyCalls++
			c.String(http.StatusOK, "legacy")
		})
		if w := serve(r, "GET", target); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "root") {
			t.Fatalf("%s harus SPA, dapat %d %q", target, w.Code, w.Body.String())
		}
	}
	if legacyCalls != 0 {
		t.Fatalf("handler HTML lama terpanggil %d kali", legacyCalls)
	}
	r.GET("/api/protected", func(c *gin.Context) { api.Fail(c, 401, "unauthenticated", "Login diperlukan.", nil) })
	r.GET("/static/app.css", func(c *gin.Context) { c.String(200, "legacy-css") })
	if w := serve(r, "GET", "/api/protected"); w.Code != 401 || !strings.Contains(w.Body.String(), "unauthenticated") {
		t.Fatalf("middleware SPA melewati proteksi API: %d %s", w.Code, w.Body.String())
	}
	if w := serve(r, "GET", "/static/app.css"); w.Code != 200 || w.Body.String() != "legacy-css" {
		t.Fatalf("asset M1 harus tetap tersedia: %d %s", w.Code, w.Body.String())
	}
}

// TestSPANoBuildKeepsLegacyRoutes memastikan development tanpa build tetap bisa memakai handler lama.
func TestSPANoBuildKeepsLegacyRoutes(t *testing.T) {
	r := gin.New()
	r.Use(api.SPA(t.TempDir()))
	r.NoRoute(api.NoRoute(""))
	r.GET("/login", func(c *gin.Context) { c.String(200, "legacy") })
	if w := serve(r, "GET", "/login"); w.Code != 200 || w.Body.String() != "legacy" {
		t.Fatalf("tanpa build harus memakai handler lama: %d %s", w.Code, w.Body.String())
	}
}

func serve(r *gin.Engine, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

// URL halaman SPA dapet index.html, asset dapet file aslinya
func TestSPAFallbackServesIndexAndAssets(t *testing.T) {
	r := newSPARouter(t)
	for _, target := range []string{"/", "/databases/1/jobs/81", "/admin/users?x=1"} {
		w := serve(r, "GET", target)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "root") {
			t.Errorf("%s harus index.html 200, dapat %d %q", target, w.Code, w.Body.String())
		}
	}
	w := serve(r, "GET", "/assets/app.js")
	if w.Code != http.StatusOK || w.Body.String() != "console.log(1)" {
		t.Errorf("asset harus dikirim apa adanya, dapat %d %q", w.Code, w.Body.String())
	}
}

// /api tetap JSON 404, method selain GET ga dapet halaman, path traversal ga bisa keluar folder
func TestSPAFallbackKeepsAPIAndSafety(t *testing.T) {
	r := newSPARouter(t)
	if w := serve(r, "GET", "/api/tidak-ada"); w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("/api harus JSON 404, dapat %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := serve(r, "POST", "/databases"); w.Code != http.StatusNotFound {
		t.Errorf("POST non-API harus 404, dapat %d", w.Code)
	}
	if w := serve(r, "GET", "/../../etc/passwd"); strings.Contains(w.Body.String(), "root:") {
		t.Error("path traversal ga boleh baca file di luar folder build")
	}
}
