package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parameter Argon2id ngikutin rekomendasi minimum OWASP:
// memory 19 MiB, 2 iterasi, 1 thread
const (
	argonMemory  = 19 * 1024 // dalam KiB
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

var errBadHash = errors.New("format hash password tidak dikenali")

// hash palsu buat dibandingin kalau username ga ketemu, biar waktu responnya sama
var dummyHash, _ = HashPassword("dummy-password")

// ubah password jadi hash Argon2id, dipakai juga waktu admin bikin atau ganti password user.
// Hasilnya format standar: $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// cek password cocok sama hash yang disimpan.
// Parameter dibaca dari hash-nya, jadi hash lama tetap bisa dicek walau konstanta di atas diganti
func CheckPassword(encoded, password string) bool {
	memory, time, threads, salt, key, err := parseHash(encoded)
	if err != nil {
		return false
	}

	candidate := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(key)))

	// waktu perbandingannya selalu sama, jadi hash ga bisa ditebak dari lama respon
	return subtle.ConstantTimeCompare(candidate, key) == 1
}

// pecah string hash jadi parameter, salt, dan hash aslinya
func parseHash(encoded string) (memory, time uint32, threads uint8, salt, key []byte, err error) {
	// bagian ke-0 kosong karena string diawali "$"
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, errBadHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, errBadHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return 0, 0, 0, nil, nil, errBadHash
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, errBadHash
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return 0, 0, 0, nil, nil, errBadHash
	}
	return memory, time, threads, salt, key, nil
}
