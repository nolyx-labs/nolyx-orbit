// HTTP plumbing shared by every handler: errors, JSON helpers, security headers and the SPA.
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type apiError struct {
	status int
	msg    string
}

func (e apiError) Error() string { return e.msg }

func errStatus(status int, format string, a ...any) error {
	return apiError{status, fmt.Sprintf(format, a...)}
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) h(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		var ae apiError
		switch {
		case err == nil:
		case errors.As(err, &ae):
			writeJSON(w, ae.status, map[string]string{"error": ae.msg})
		case errors.Is(err, pgx.ErrNoRows):
			writeJSON(w, 404, map[string]string{"error": "not found"})
		default:
			s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if raw, ok := v.(json.RawMessage); ok {
		w.Write(raw)
		return
	}
	json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errStatus(415, "expected application/json")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errStatus(400, "invalid body: %v", err)
	}
	return nil
}

// list and one let Postgres build the JSON, which keeps handlers free of scan boilerplate.
// The CTE form works for both SELECT and INSERT/UPDATE ... RETURNING.
func (s *Server) list(ctx context.Context, sql string, args ...any) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.db.QueryRow(ctx, `with _r as (`+sql+`) select coalesce(json_agg(_r), '[]') from _r`, args...).Scan(&out)
	return out, err
}

func (s *Server) one(ctx context.Context, sql string, args ...any) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.db.QueryRow(ctx, `with _r as (`+sql+`) select row_to_json(_r) from _r`, args...).Scan(&out)
	return out, err
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return securityHeaders(s.customDomains(mux), strings.HasPrefix(s.cfg.AppURL, "https://"))
}

func securityHeaders(next http.Handler, https bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if https {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// spa serves the built web UI, falling back to index.html for client-side routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.cfg.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(s.cfg.Web, p); p == "" || err != nil {
			index, err := fs.ReadFile(s.cfg.Web, "index.html")
			if err != nil {
				http.Error(w, "web UI not built (run: cd web && npm run build)", 503)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(index)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
