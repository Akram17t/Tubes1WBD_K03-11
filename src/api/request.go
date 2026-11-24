package api

import (
	"bytes"
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

var (
	ErrInvalidInput    = errors.New("input JSON atau parameter tidak valid")
	errJSONContentType = errors.New("Content-Type harus application/json.")
	errJSONObject      = errors.New("Body harus berisi objek JSON.")
	errJSONMultiple    = errors.New("Body hanya boleh berisi satu objek JSON.")
)

// baca body JSON ke dst. Wajib Content-Type application/json, field asing ditolak,
// kalau gagal langsung balas 400 invalid_input dan return false
func BindJSON(c *gin.Context, dst any) bool {
	if err := decodeJSON(c, dst, false); err != nil {
		Fail(c, http.StatusBadRequest, CodeInvalidInput, jsonErrorMessage(err), nil)
		return false
	}
	return true
}

// Decode memakai parser yang sama dengan BindJSON, tetapi pemetaan error tetap ditangani modul pemanggil.
func Decode(c *gin.Context, dst any, allowEmpty bool) error {
	if err := decodeJSON(c, dst, allowEmpty); err != nil {
		return errors.Join(ErrInvalidInput, err)
	}
	return nil
}

// decodeJSON menolak null, array, field asing, dan payload tambahan; hanya aksi tanpa input boleh kosong.
func decodeJSON(c *gin.Context, dst any, allowEmpty bool) error {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBodyBytes))
	if err != nil {
		return err
	}
	body = bytes.TrimSpace(body)
	if allowEmpty && len(body) == 0 {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errJSONContentType
	}
	if len(body) == 0 {
		return io.EOF
	}
	if body[0] != '{' {
		return errJSONObject
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errJSONMultiple
	}
	return nil
}

// pesan error JSON yang ramah buat ditampilin di SPA
func jsonErrorMessage(err error) string {
	if errors.Is(err, errJSONContentType) || errors.Is(err, errJSONObject) || errors.Is(err, errJSONMultiple) {
		return err.Error()
	}
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
