package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"tubes-wbd/src/telemetry"

	"github.com/gin-gonic/gin"
)

var ErrInvalidInput = errors.New("input JSON atau parameter tidak valid")

// IsRequest membedakan request API dari halaman HTML M1.
func IsRequest(c *gin.Context) bool {
	return c.Request.URL.Path == "/api" || strings.HasPrefix(c.Request.URL.Path, "/api/")
}

// Meta menggunakan correlation ID middleware atau membuatnya bila belum tersedia.
func Meta(c *gin.Context) gin.H {
	id := telemetry.RequestID(c.Request.Context())
	if id == "" {
		id = c.GetString("request_id")
	}
	if id == "" {
		id = "http-" + rand.Text()
		c.Set("request_id", id)
	}
	c.Header("X-Request-ID", id)
	return gin.H{"request_id": id}
}

// Respond mengirim hasil dengan format data dan metadata yang sama untuk setiap endpoint.
func Respond(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data, "meta": Meta(c)})
}

// Abort menghentikan request dengan error JSON tanpa membocorkan detail internal.
func Abort(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{"code": code, "message": message}, "meta": Meta(c),
	})
}

// Decode menerima satu object JSON berukuran terbatas dan menolak field yang tidak dikenal.
func Decode(c *gin.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		return ErrInvalidInput
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	encoded, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ErrInvalidInput
	}
	encoded = bytes.TrimSpace(encoded)
	if len(encoded) == 0 || encoded[0] != '{' {
		return ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidInput
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrInvalidInput
	}
	return nil
}

// Pagination membatasi jumlah hasil dan menolak offset negatif atau angka yang tidak valid.
func Pagination(c *gin.Context) (int, int, error) {
	limit, errLimit := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, errOffset := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if errLimit != nil || errOffset != nil || limit < 1 || limit > 500 || offset < 0 {
		return 0, 0, ErrInvalidInput
	}
	return limit, offset, nil
}
