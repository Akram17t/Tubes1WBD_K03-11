package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// batas ukuran body JSON biar request raksasa ga bikin server kehabisan memory
const maxJSONBodyBytes = 1 << 20

// baca body JSON ke dst. Wajib Content-Type application/json, field asing ditolak,
// kalau gagal langsung balas 400 invalid_input dan return false
func BindJSON(c *gin.Context, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Fail(c, http.StatusBadRequest, CodeInvalidInput, "Content-Type harus application/json.", nil)
		return false
	}

	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		Fail(c, http.StatusBadRequest, CodeInvalidInput, jsonErrorMessage(err), nil)
		return false
	}
	// body harus berisi tepat satu objek JSON, sisa data dianggap ga valid
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		Fail(c, http.StatusBadRequest, CodeInvalidInput, "Body hanya boleh berisi satu objek JSON.", nil)
		return false
	}
	return true
}

// pesan error JSON yang ramah buat ditampilin di SPA
func jsonErrorMessage(err error) string {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return "Body request terlalu besar."
	}
	if errors.Is(err, io.EOF) {
		return "Body request kosong."
	}
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && typeErr.Field != "" {
		return "Tipe data field " + typeErr.Field + " tidak sesuai."
	}
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return "Field " + field + " tidak dikenal."
	}
	return "Body request bukan JSON yang valid."
}
