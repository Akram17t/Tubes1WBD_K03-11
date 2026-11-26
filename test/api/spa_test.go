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
	r.NoRoute(api.NoRoute(dir))
	return r
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
