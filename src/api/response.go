// Package api berisi format response JSON bersama untuk semua endpoint /api.
package api

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"tubes-wbd/src/telemetry"

	"github.com/gin-gonic/gin"
)

// Kode error yang dipakai di field error.code, sesuai tabel kontrak M2 bagian 9.1.
const (
	CodeInvalidInput       = "invalid_input"
	CodeInvalidSQL         = "invalid_sql"
	CodeUnauthenticated    = "unauthenticated"
	CodeForbidden          = "forbidden"
	CodeCSRFInvalid        = "csrf_invalid"
	CodeNotFound           = "not_found"
	CodeStateConflict      = "state_conflict"
	CodeApprovalIncomplete = "approval_incomplete"
	CodeImmutableMigration = "immutable_migration"
	CodeDatabaseBlocked    = "database_blocked"
	CodeNotReversible      = "not_reversible"
	CodeTargetUnavailable  = "target_unavailable"
	CodeInternal           = "internal_error"
)

// Pesan default yang aman ditampilkan ke user.
const (
	internalErrorMessage    = "Terjadi kesalahan internal pada server."
	notFoundMessage         = "Resource tidak ditemukan."
	invalidPaginationReason = "limit harus 1 sampai 500 dan offset tidak boleh negatif."
)

// Batas pagination default semua endpoint list.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Meta ikut di setiap response, minimal request_id buat nyocokin dengan log.
type Meta struct {
	RequestID string `json:"request_id"`
	Limit     *int   `json:"limit,omitempty"`
	Offset    *int   `json:"offset,omitempty"`
	HasMore   *bool  `json:"has_more,omitempty"`
}

// ErrorBody isi field error, details opsional buat info tambahan ke SPA.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type dataResponse struct {
	Data any  `json:"data"`
	Meta Meta `json:"meta"`
}

type errorResponse struct {
	Error ErrorBody `json:"error"`
	Meta  Meta      `json:"meta"`
}

// Page hasil parsing query limit/offset.
type Page struct {
	Limit  int
	Offset int
}

// ambil meta dasar dari context request (request_id dari middleware)
func baseMeta(c *gin.Context) Meta {
	id := telemetry.RequestID(c.Request.Context())
	if id == "" {
		id = "http-" + rand.Text()
		c.Request = c.Request.WithContext(telemetry.WithRequestID(c.Request.Context(), id))
	}
	c.Header(telemetry.RequestIDHeader, id)
	return Meta{RequestID: id}
}

// kirim response sukses {data, meta}, data nil jadi null (misal logout)
func JSON(c *gin.Context, status int, data any) {
	c.JSON(status, dataResponse{Data: emptySliceIfNil(data), Meta: baseMeta(c)})
}

// kirim response list, slice nil otomatis jadi [] biar SPA ga dapet null
func List(c *gin.Context, items any, page Page, hasMore bool) {
	meta := baseMeta(c)
	meta.Limit, meta.Offset, meta.HasMore = &page.Limit, &page.Offset, &hasMore
	if items == nil {
		items = []any{}
	}
	c.JSON(http.StatusOK, dataResponse{Data: emptySliceIfNil(items), Meta: meta})
}

// kirim error {error, meta} lalu abort, handler sesudahnya ga dijalanin
func Fail(c *gin.Context, status int, code, message string, details map[string]any) {
	c.AbortWithStatusJSON(status, errorResponse{
		Error: ErrorBody{Code: code, Message: message, Details: details},
		Meta:  baseMeta(c),
	})
}

// error 500 generik, detail error asli cuma masuk log biar ga bocor ke client
func Internal(c *gin.Context, err error) {
	slog.ErrorContext(c.Request.Context(), "http.internal_error",
		"route", routeOf(c), "error", errString(err))
	Fail(c, http.StatusInternalServerError, CodeInternal, internalErrorMessage, nil)
}

// helper 404 standar
func NotFound(c *gin.Context) {
	Fail(c, http.StatusNotFound, CodeNotFound, notFoundMessage, nil)
}

// baca ?limit=&offset=, default 50/0. Kalau ga valid langsung balas 400 dan ok=false
func ParsePage(c *gin.Context) (Page, bool) {
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(DefaultLimit)))
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limitErr != nil || offsetErr != nil || limit < 1 || limit > MaxLimit || offset < 0 {
		Fail(c, http.StatusBadRequest, CodeInvalidInput, invalidPaginationReason, nil)
		return Page{}, false
	}
	return Page{Limit: limit, Offset: offset}, true
}

// ID BIGINT dikirim sebagai string desimal biar aman di JavaScript
func FormatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

// kebalikan FormatID, cuma nerima bilangan positif
func ParseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id harus bilangan bulat positif")
	}
	return id, nil
}

// ambil ID dari path param (misal :user_id), balas 404 kalau formatnya ngaco
func PathID(c *gin.Context, name string) (int64, bool) {
	id, err := ParseID(c.Param(name))
	if err != nil {
		NotFound(c)
		return 0, false
	}
	return id, true
}

// true kalau path request masuk wilayah /api
func IsAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// kalau value-nya slice nil, ganti jadi slice kosong dengan tipe yang sama
func emptySliceIfNil(value any) any {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Slice && v.IsNil() {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return value
}

// route pattern (bukan URL asli) biar log ga kebanyakan variasi
func routeOf(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "<unmatched>"
}

// err.Error() yang aman buat err nil
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
