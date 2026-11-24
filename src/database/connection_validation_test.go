package database

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tubes-wbd/src/api"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestConnectionValidationMessagesReachAPI menjaga pesan spesifik dari validator sampai response JSON.
func TestConnectionValidationMessagesReachAPI(t *testing.T) {
	for _, tc := range []struct{ field, value, message string }{
		{"name", "", "nama wajib diisi"},
		{"host", "", "host wajib diisi"},
		{"host", "local host", "host tidak boleh mengandung spasi"},
		{"host", "local\thost", "host tidak boleh mengandung spasi"},
		{"host", "local\x00host", "mengandung karakter tidak valid"},
		{"database", "", "nama database wajib diisi"},
		{"username", "", "username database wajib diisi"},
		{"port", "", "port harus 1 sampai 65535"},
		{"ssl", "unknown", "ssl_mode tidak dikenal"},
		{"password", "", "password wajib diisi"},
	} {
		t.Run(tc.field+tc.value, func(t *testing.T) {
			input := ConnectionInput{Name: "Demo", Host: "localhost", Port: 5432,
				DatabaseName: "demo", DBUsername: "postgres", SSLMode: "disable", Password: "private-test-secret"}
			switch tc.field {
			case "name":
				input.Name = tc.value
			case "host":
				input.Host = tc.value
			case "database":
				input.DatabaseName = tc.value
			case "username":
				input.DBUsername = tc.value
			case "port":
				input.Port = 0
			case "ssl":
				input.SSLMode = tc.value
			case "password":
				input.Password = tc.value
			}
			_, err := validateConnection(input, true)
			if !errors.Is(err, ErrInvalidConnection) {
				t.Fatalf("input invalid harus ditolak: %v", err)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/connections", nil)
			databaseAPIError(c, err)
			var body struct{ Error api.ErrorBody }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 400 ||
				body.Error.Code != api.CodeInvalidInput || !strings.Contains(body.Error.Message, tc.message) {
				t.Fatalf("pesan validasi hilang: %d %s, %v", w.Code, w.Body.String(), err)
			}
			if strings.Contains(w.Body.String(), "private-test-secret") {
				t.Fatal("password tidak boleh masuk pesan validasi")
			}
		})
	}
}

// TestConnectionInternalErrorsStayGeneric memastikan perbaikan validasi tidak membuka pesan driver atau credential.
func TestConnectionInternalErrorsStayGeneric(t *testing.T) {
	for _, inputErr := range []error{api.ErrInvalidInput, ErrTargetUnavailable,
		errors.New("password=private-test-secret"), &pgconn.PgError{Code: "28P01", Message: "password=private-test-secret"}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/connections", nil)
		databaseAPIError(c, inputErr)
		if !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "private-test-secret") {
			t.Fatalf("error internal bocor: %s", w.Body.String())
		}
		if errors.Is(inputErr, api.ErrInvalidInput) && !strings.Contains(w.Body.String(), "Input JSON atau parameter tidak valid.") {
			t.Fatal("JSON invalid harus tetap mendapat pesan umum")
		}
	}
}
