// Brute-force protection for sign-in, sign-up and password resets.
package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const attemptWindow = 15 * time.Minute

// attempts counts failures per key in fixed windows. In memory: Orbit runs as a single instance
// (docs/ARCHITECTURE.md §7), and a restart only resets the counters.
type attempts struct {
	mu sync.Mutex
	m  map[string]*attemptCount
}

type attemptCount struct {
	n     int
	until time.Time
}

func (a *attempts) blocked(key string, max int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.m[key]
	return c != nil && c.n >= max && time.Now().Before(c.until)
}

func (a *attempts) fail(keys ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if a.m == nil {
		a.m = map[string]*attemptCount{}
	}
	if len(a.m) > 10_000 {
		for k, c := range a.m {
			if now.After(c.until) {
				delete(a.m, k)
			}
		}
	}
	for _, k := range keys {
		c := a.m[k]
		if c == nil || now.After(c.until) {
			c = &attemptCount{until: now.Add(attemptWindow)}
			a.m[k] = c
		}
		c.n++
	}
}

func (a *attempts) clear(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.m, key)
}

// clientIP is the caller's address. Behind a proxy on a private network (the bundled Caddy, or your own),
// it's the hop the proxy appended to X-Forwarded-For; a client can't forge that last entry.
func clientIP(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a := net.ParseIP(ip); a != nil && (a.IsLoopback() || a.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return ip
}
