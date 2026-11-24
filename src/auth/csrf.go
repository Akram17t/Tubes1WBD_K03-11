package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/url"

	"tubes-wbd/src/api"

	"github.com/gin-gonic/gin"
)

// Token CSRF = nonce acak + tanda tangan HMAC(csrf_secret session, nonce).
// Nonce-nya baru tiap render, jadi token kelihatan beda terus, tapi semuanya tetap valid selama session-nya sama.
const (
	csrfFieldName = "csrf_token"
	csrfNonceSize = 16
)

// bikin tanda tangan HMAC-SHA256 dari nonce pakai secret session
func signNonce(secret, nonce []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(nonce)
	return mac.Sum(nil)
}

// bikin token CSRF baru buat ditaruh di hidden field form
func makeCSRFToken(secret []byte) string {
	nonce := make([]byte, csrfNonceSize)
	rand.Read(nonce)

	signature := signNonce(secret, nonce)

	token := make([]byte, 0, len(nonce)+len(signature))
	token = append(token, nonce...)
	token = append(token, signature...)
	return base64.RawURLEncoding.EncodeToString(token)
}

// cek token dari form beneran dibuat dari secret session ini
func validCSRFToken(secret []byte, token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	if len(raw) != csrfNonceSize+sha256.Size {
		return false
	}

	nonce := raw[:csrfNonceSize]
	signature := raw[csrfNonceSize:]

	// hmac.Equal waktunya selalu sama, jadi token ga bisa ditebak dari lama responnya
	return hmac.Equal(signature, signNonce(secret, nonce))
}

// ambil token CSRF buat template, kosong kalau belum login, taruh di form.
func CSRFToken(c *gin.Context) string {
	session, ok := currentSession(c)
	if !ok {
		return ""
	}
	return makeCSRFToken(session.CSRFSecret)
}

// cek request datang dari halaman kita sendiri lewat header Origin, kalau Origin ga ada pakai Referer
func sameOrigin(r *http.Request) bool {
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Header.Get("Referer")
	}
	if source == "" {
		return false
	}

	sourceURL, err := url.Parse(source)
	if err != nil {
		return false
	}
	return sourceURL.Host == r.Host
}

// CSRFHeader header tempat SPA ngirim token CSRF di setiap request yang mengubah data.
const CSRFHeader = "X-CSRF-Token"

// middleware yang nolak POST/PATCH/DELETE tanpa token CSRF valid, pasang setelah Session.
// Login (HTML maupun API) dicek lewat Origin karena belum punya session
func CSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if path == "/login" || path == LoginPath {
			if !sameOrigin(c.Request) {
				rejectCSRF(c, "origin_mismatch")
				return
			}
			c.Next()
			return
		}

		session, ok := currentSession(c)
		if !ok {
			c.Next()
			return
		}

		// SPA kirim token lewat header, halaman lama lewat hidden field form
		var token string
		if api.IsAPIPath(path) {
			token = c.GetHeader(CSRFHeader)
		} else {
			token = c.PostForm(csrfFieldName)
		}
		if !validCSRFToken(session.CSRFSecret, token) {
			rejectCSRF(c, "invalid_token")
			return
		}
		c.Next()
	}
}

// tolak request CSRF: catat ke log (tanpa isi token), /api dapet JSON 403 csrf_invalid
func rejectCSRF(c *gin.Context, reason string) {
	slog.WarnContext(c.Request.Context(), "auth.csrf_rejected", "reason", reason, "method", c.Request.Method, "path", c.FullPath())
	if api.IsAPIPath(c.Request.URL.Path) {
		api.Fail(c, http.StatusForbidden, api.CodeCSRFInvalid, "Token CSRF tidak valid. Muat ulang halaman lalu coba lagi.", nil)
		return
	}
	c.AbortWithStatus(http.StatusForbidden)
}
