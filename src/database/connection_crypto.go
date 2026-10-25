package database

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
)

const passwordEnvelope = "v1:"

func (s *ConnectionService) encryptPassword(password string) (string, error) {
	nonce := make([]byte, s.passwordCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrConnectionCredentials
	}
	sealed := s.passwordCipher.Seal(nonce, nonce, []byte(password), []byte(passwordEnvelope))
	return passwordEnvelope + base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *ConnectionService) decryptPassword(encrypted string) (string, error) {
	if !strings.HasPrefix(encrypted, passwordEnvelope) {
		return "", ErrConnectionCredentials
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encrypted, passwordEnvelope))
	nonceSize := s.passwordCipher.NonceSize()
	if err != nil || len(raw) < nonceSize+s.passwordCipher.Overhead() {
		return "", ErrConnectionCredentials
	}
	plain, err := s.passwordCipher.Open(nil, raw[:nonceSize], raw[nonceSize:], []byte(passwordEnvelope))
	if err != nil {
		return "", ErrConnectionCredentials
	}
	return string(plain), nil
}
