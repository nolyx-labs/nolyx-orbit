package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestTOTPRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890") // RFC 6238 appendix B, SHA-1; last 6 of the 8-digit values
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 2000000000: "279037"} {
		if got := totp(secret, unix/30); got != want {
			t.Errorf("totp(%d) = %s, want %s", unix, got, want)
		}
	}
	now := time.Unix(1111111109, 0)
	if _, ok := totpMatch(secret, totp(secret, now.Unix()/30-1), now); !ok {
		t.Error("previous step rejected")
	}
	if _, ok := totpMatch(secret, totp(secret, now.Unix()/30-2), now); ok {
		t.Error("step two behind accepted")
	}
}

func TestAttempts(t *testing.T) {
	var a attempts
	for range 3 {
		a.fail("ip:1", "email:x")
	}
	if !a.blocked("email:x", 3) || a.blocked("email:x", 4) || a.blocked("email:y", 1) {
		t.Fatal("unexpected limiter state")
	}
	a.clear("email:x")
	if a.blocked("email:x", 1) || !a.blocked("ip:1", 3) {
		t.Fatal("clear removed the wrong key")
	}
	a.m["ip:1"].until = time.Now().Add(-time.Second)
	if a.blocked("ip:1", 3) {
		t.Fatal("expired window still blocks")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:4000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := clientIP(r); got != "203.0.113.5" {
		t.Errorf("public peer trusted X-Forwarded-For: %s", got)
	}
	r.RemoteAddr = "172.18.0.3:4000" // the bundled Caddy
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.7")
	if got := clientIP(r); got != "198.51.100.7" {
		t.Errorf("behind proxy = %s, want the hop the proxy appended", got)
	}
}
