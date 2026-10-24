package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"

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

// middleware yang nolak POST tanpa token CSRF yang valid, pasang setelah Session. POST /login dicek lewat Origin karena belum punya session.
func CSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			c.Next()
			return
		}

		if c.Request.URL.Path == "/login" {
			if !sameOrigin(c.Request) {
				c.AbortWithStatus(http.StatusForbidden)
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

		if !validCSRFToken(session.CSRFSecret, c.PostForm(csrfFieldName)) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
