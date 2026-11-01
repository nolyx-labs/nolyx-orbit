package server

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

func TestEncryptRoundTripAndTamper(t *testing.T) {
	key, err := decodeKey("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	s := &Server{aead: aead}
	ct := s.encrypt([]byte(`{"DATABASE_URL":"postgres://x"}`))
	pt, err := s.decrypt(ct)
	if err != nil || !bytes.Equal(pt, []byte(`{"DATABASE_URL":"postgres://x"}`)) {
		t.Fatalf("round trip failed: %v %q", err, pt)
	}
	ct[len(ct)-1] ^= 1
	if _, err := s.decrypt(ct); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
	if _, err := decodeKey("short"); err == nil {
		t.Fatal("short key accepted")
	}
}
