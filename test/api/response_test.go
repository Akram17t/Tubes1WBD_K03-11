package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"tubes-wbd/src/api"
	"tubes-wbd/src/telemetry"
)

// router test dengan urutan middleware yang sama kayak routes.go
func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(telemetry.RequestIDMiddleware(), telemetry.TraceMiddleware(), api.Recovery())
	r.NoRoute(api.NoRoute())
	return r
}

// kirim request lalu decode body JSON-nya ke map
func call(t *testing.T, r *gin.Engine, method, target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	var body map[string]any
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body bukan JSON valid: %v (%s)", err, w.Body.String())
		}
	}
	return w, body
}

func TestJSONResponseHasDataAndRequestID(t *testing.T) {
	r := newRouter()
	r.GET("/api/thing", func(c *gin.Context) { api.JSON(c, http.StatusOK, map[string]string{"id": api.FormatID(81)}) })
	w, body := call(t, r, "GET", "/api/thing")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	data := body["data"].(map[string]any)
	if data["id"] != "81" {
		t.Errorf("id BIGINT harus string: %v", data["id"])
	}
	meta := body["meta"].(map[string]any)
	requestID, _ := meta["request_id"].(string)
	if !strings.HasPrefix(requestID, "http-") || w.Header().Get(telemetry.RequestIDHeader) != requestID {
		t.Errorf("request_id meta %q tidak sama dengan header %q", requestID, w.Header().Get(telemetry.RequestIDHeader))
	}
}

func TestRequestIDFromClientIgnored(t *testing.T) {
	r := newRouter()
	r.GET("/api/thing", func(c *gin.Context) { api.JSON(c, http.StatusOK, nil) })
	req := httptest.NewRequest("GET", "/api/thing", nil)
	req.Header.Set(telemetry.RequestIDHeader, "palsu")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Header().Get(telemetry.RequestIDHeader) == "palsu" {
		t.Fatal("request ID dari client tidak boleh dipakai")
	}
	if !strings.Contains(w.Body.String(), `"data":null`) {
		t.Errorf("data nil harus jadi null: %s", w.Body.String())
	}
}

func TestListNilSliceBecomesEmptyArray(t *testing.T) {
	r := newRouter()
	r.GET("/api/items", func(c *gin.Context) {
		page, ok := api.ParsePage(c)
		if !ok {
			return
		}
		var items []string
		api.List(c, items, page, false)
	})
	w, body := call(t, r, "GET", "/api/items")
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("slice nil harus jadi []: %s", w.Body.String())
	}
	meta := body["meta"].(map[string]any)
	if meta["limit"] != float64(50) || meta["offset"] != float64(0) || meta["has_more"] != false {
		t.Errorf("meta pagination default salah: %v", meta)
	}
}

func TestParsePageRejectsInvalid(t *testing.T) {
	r := newRouter()
	r.GET("/api/items", func(c *gin.Context) {
		if page, ok := api.ParsePage(c); ok {
			api.List(c, []int{}, page, true)
		}
	})
	for _, query := range []string{"limit=0", "limit=501", "limit=abc", "offset=-1", "offset=x"} {
		w, body := call(t, r, "GET", "/api/items?"+query)
		if w.Code != 400 || body["error"].(map[string]any)["code"] != api.CodeInvalidInput {
			t.Errorf("%s: harus 400 invalid_input, dapat %d %v", query, w.Code, body)
		}
	}
	if w, body := call(t, r, "GET", "/api/items?limit=500&offset=10"); w.Code != 200 || body["meta"].(map[string]any)["limit"] != float64(500) {
		t.Errorf("limit 500 harus diterima: %d %v", w.Code, body)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	r := newRouter()
	w, body := call(t, r, "GET", "/api/tidak-ada")
	if w.Code != 404 || body["error"].(map[string]any)["code"] != api.CodeNotFound {
		t.Fatalf("harus JSON 404: %d %s", w.Code, w.Body.String())
	}
	if body["meta"].(map[string]any)["request_id"] == "" {
		t.Error("error 404 tetap harus punya request_id")
	}
	if w, _ := call(t, r, "GET", "/halaman-lama"); w.Code != 404 || strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("route non-API tetap 404 teks: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestPanicInAPIReturnsSafeJSON(t *testing.T) {
	r := newRouter()
	r.GET("/api/boom", func(c *gin.Context) { panic("password=rahasia") })
	r.GET("/boom", func(c *gin.Context) { panic("x") })
	w, body := call(t, r, "GET", "/api/boom")
	if w.Code != 500 || body["error"].(map[string]any)["code"] != api.CodeInternal {
		t.Fatalf("panic API harus JSON 500: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "rahasia") {
		t.Error("isi panic bocor ke response")
	}
	if w, _ := call(t, r, "GET", "/boom"); w.Code != 500 {
		t.Errorf("panic non-API harus 500: %d", w.Code)
	}
}

func TestPathIDRejectsInvalid(t *testing.T) {
	r := newRouter()
	r.GET("/api/users/:user_id", func(c *gin.Context) {
		if id, ok := api.PathID(c, "user_id"); ok {
			api.JSON(c, http.StatusOK, api.FormatID(id))
		}
	})
	for _, id := range []string{"0", "-5", "abc", "1.5", "99999999999999999999"} {
		if w, _ := call(t, r, "GET", "/api/users/"+id); w.Code != 404 {
			t.Errorf("id %q harus 404, dapat %d", id, w.Code)
		}
	}
	if w, body := call(t, r, "GET", "/api/users/9007199254740993"); w.Code != 200 || body["data"] != "9007199254740993" {
		t.Errorf("ID besar harus utuh sebagai string: %d %v", w.Code, body["data"])
	}
}
