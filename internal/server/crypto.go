// Encryption at rest and credential tokens.
package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

func (s *Server) encrypt(plain []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plain, nil)
}

func (s *Server) decrypt(b []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("ciphertext too short")
	}
	return s.aead.Open(nil, b[:n], b[n:], nil)
}

// newToken returns a prefixed random token and its SHA-256 hash (only the hash is stored).
func newToken(prefix string) (string, []byte) {
	b := make([]byte, 32)
	rand.Read(b)
	t := prefix + base64.RawURLEncoding.EncodeToString(b)
	return t, hashToken(t)
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

func randomSecret() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
