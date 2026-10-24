package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// token yang dibuat dari secret yang sama harus lolos, dan tiap token beda-beda
func TestCSRFTokenValidAndRandom(t *testing.T) {
	secret := []byte("secret-session-a")

	first := makeCSRFToken(secret)
	second := makeCSRFToken(secret)

	if first == second {
		t.Error("dua token harusnya beda karena nonce-nya acak")
	}
	if !validCSRFToken(secret, first) || !validCSRFToken(secret, second) {
		t.Error("token dari secret yang sama harusnya valid")
	}
}

// token palsu, rusak, kosong, atau dari session lain harus ditolak
func TestCSRFTokenRejected(t *testing.T) {
	secret := []byte("secret-session-a")
	otherSecret := []byte("secret-session-b")
	good := makeCSRFToken(secret)

	// ubah satu karakter terakhir biar tanda tangannya rusak
	lastChar := "A"
	if good[len(good)-1] == 'A' {
		lastChar = "B"
	}
	tampered := good[:len(good)-1] + lastChar

	tests := []struct {
		name  string
		token string
	}{
		{"kosong", ""},
		{"bukan base64", "ini bukan token!!"},
		{"kependekan", "YWJj"},
		{"diubah", tampered},
		{"dari session lain", makeCSRFToken(otherSecret)},
	}

	for _, tt := range tests {
		if validCSRFToken(secret, tt.token) {
			t.Errorf("%s: harusnya ditolak", tt.name)
		}
	}
}

// Origin atau Referer harus sama dengan host aplikasi
func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		referer string
		want    bool
	}{
		{"origin sendiri", "http://example.com", "", true},
		{"origin situs lain", "http://situs-jahat.com", "", false},
		{"origin null", "null", "", false},
		{"tanpa origin, referer sendiri", "", "http://example.com/login", true},
		{"tanpa origin, referer situs lain", "", "http://situs-jahat.com/x", false},
		{"ga ada keduanya", "", "", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		if tt.referer != "" {
			req.Header.Set("Referer", tt.referer)
		}
		if got := sameOrigin(req); got != tt.want {
			t.Errorf("%s: dapat %v, harusnya %v", tt.name, got, tt.want)
		}
	}
}
