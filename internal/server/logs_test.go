package server

import "testing"

func TestLogLevel(t *testing.T) {
	cases := map[string]string{
		"ERROR connection refused":           "error",
		`{"level":"warn","msg":"slow"}`:      "warn",
		"panic: runtime error":               "error",
		"GET /health 200":                    "info",
		"[debug] cache miss":                 "debug",
		`{"severity":"INFO","error":"none"}`: "info",
	}
	for line, want := range cases {
		if got := logLevel(line); got != want {
			t.Errorf("logLevel(%q) = %s, want %s", line, got, want)
		}
	}
}
