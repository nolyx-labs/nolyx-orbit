package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExec(t *testing.T) {
	exit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1.43/containers/c1/exec":
			w.Write([]byte(`{"Id":"e1"}`))
		case r.URL.Path == "/v1.43/exec/e1/start":
			w.Write(append([]byte{1, 0, 0, 0, 0, 0, 0, 6}, "hello\n"...))
		case r.URL.Path == "/v1.43/exec/e1/json":
			w.Write([]byte(`{"ExitCode":` + map[int]string{0: "0", 2: "2"}[exit] + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d := &Docker{http: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}}}}
	if out, err := d.Exec(context.Background(), "c1", []string{"echo", "hello"}, time.Second); err != nil || out != "hello\n" {
		t.Fatalf("got %q %v", out, err)
	}
	exit = 2
	if _, err := d.Exec(context.Background(), "c1", []string{"false"}, time.Second); err == nil || !strings.Contains(err.Error(), "exit code 2\nhello") {
		t.Fatalf("non-zero exit: %v", err)
	}
}
