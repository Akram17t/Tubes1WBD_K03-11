package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tubes-wbd/src/api"

	"github.com/gin-gonic/gin"
)

// TestDecodeRejectsUnsafeOrAmbiguousBodies menjaga validasi JSON yang dipakai semua modul API.
func TestDecodeRejectsUnsafeOrAmbiguousBodies(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		allowEmpty, valid       bool
	}{
		{"object", `{"id":"1"}`, "application/json; charset=utf-8", false, true},
		{"empty action", "", "", true, true},
		{"empty required", "", "application/json", false, false},
		{"unknown", `{"sql":"DROP TABLE users"}`, "application/json", false, false},
		{"action payload", `{"sql":"SELECT 1"}`, "application/json", true, false},
		{"multiple", `{"id":"1"} {}`, "application/json", false, false},
		{"trailing garbage", `{"id":"1"} x`, "application/json", false, false},
		{"array", `[]`, "application/json", false, false},
		{"null", `null`, "application/json", false, false},
		{"form", `id=1`, "application/x-www-form-urlencoded", false, false},
		{"too large", `{"id":"` + strings.Repeat("a", 1<<20) + `"}`, "application/json", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			var input struct {
				ID string `json:"id"`
			}
			if err := api.Decode(c, &input, tc.allowEmpty); (err == nil) != tc.valid {
				t.Fatalf("Decode: %v, expected valid=%v", err, tc.valid)
			}
		})
	}
}

// TestBindJSONUsesStrictSharedDecoder menjaga validasi auth/user ketika parser bersama dipakai ulang.
func TestBindJSONUsesStrictSharedDecoder(t *testing.T) {
	for _, body := range []string{"null", "[]", `{"id":"1"} {}`, `{"unknown":true}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var input struct {
			ID string `json:"id"`
		}
		if api.BindJSON(c, &input) || w.Code != 400 {
			t.Fatalf("payload tidak valid diterima: %s (%d)", body, w.Code)
		}
	}
}

// TestResponseWithoutMiddlewareKeepsOneRequestID memastikan response tetap punya korelasi tanpa middleware.
func TestResponseWithoutMiddlewareKeepsOneRequestID(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.Header.Set("X-Request-ID", "untrusted")
	api.JSON(c, http.StatusOK, nil)
	id := c.Writer.Header().Get("X-Request-ID")
	if id == "" || id == "untrusted" {
		t.Fatalf("missing generated request ID: %q", id)
	}
}
