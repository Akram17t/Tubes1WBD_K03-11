package auth_test

import (
	"strings"
	"testing"

	"tubes-wbd/src/auth"
)

// hash pakai format Argon2id standar, password yang sama tetap dapat hash beda karena salt acak
func TestHashPasswordUsesArgon2id(t *testing.T) {
	first, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := auth.HashPassword("rahasia123")

	if !strings.HasPrefix(first, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("format hash ga sesuai: %s", first)
	}
	if first == second {
		t.Error("dua hash dari password yang sama harusnya beda karena salt-nya acak")
	}
	if strings.Contains(first, "rahasia123") {
		t.Error("hash ga boleh berisi password asli")
	}
}

// password benar lolos, password salah dan hash rusak ditolak
func TestCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(hash, "rahasia123") {
		t.Error("password benar harusnya lolos")
	}

	parts := strings.Split(hash, "$")
	tests := []struct {
		name     string
		hash     string
		password string
	}{
		{"password salah", hash, "rahasia124"},
		{"password kosong", hash, ""},
		{"hash kosong", "", "rahasia123"},
		{"hash bcrypt lama", "$2a$10$.OE.M44YbV9QChePQOuc1u7Kh/fMUcDa74/KUpchN6QhI1sf93Suu", "demo12345"},
		{"bukan argon2id", strings.Replace(hash, "argon2id", "argon2i", 1), "rahasia123"},
		{"versi beda", strings.Replace(hash, "v=19", "v=16", 1), "rahasia123"},
		{"parameter rusak", strings.Replace(hash, "m=19456", "m=abc", 1), "rahasia123"},
		{"salt bukan base64", strings.Join([]string{"", parts[1], parts[2], parts[3], "!!!", parts[5]}, "$"), "rahasia123"},
		{"hash diubah", hash[:len(hash)-2] + "AA", "rahasia123"},
	}
	for _, tt := range tests {
		if auth.CheckPassword(tt.hash, tt.password) {
			t.Errorf("%s: harusnya ditolak", tt.name)
		}
	}
}
