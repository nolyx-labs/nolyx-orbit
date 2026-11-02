package uptime

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orbit/internal/protocol"
)

func TestStatusMatches(t *testing.T) {
	for spec, codes := range map[string][2][]int{
		"2xx":         {{200, 204}, {301, 500}},
		"200-299,301": {{250, 301}, {302, 404}},
		"404":         {{404}, {200}},
	} {
		for _, c := range codes[0] {
			if !StatusMatches(spec, c) {
				t.Errorf("%s should match %d", spec, c)
			}
		}
		for _, c := range codes[1] {
			if StatusMatches(spec, c) {
				t.Errorf("%s shouldn't match %d", spec, c)
			}
		}
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello orbit")) }))
	defer srv.Close()
	m := protocol.MonitorConfig{ID: "m", Type: "http", Target: srv.URL, ExpectedStatus: "2xx", Keyword: "orbit", TimeoutMS: 2000}
	if r := Check(context.Background(), m, &net.Dialer{}); !r.OK || r.StatusCode != 200 || r.MonitorID != "m" {
		t.Fatalf("got %+v", r)
	}
	m.Keyword = "missing"
	if r := Check(context.Background(), m, &net.Dialer{}); r.OK {
		t.Fatal("keyword check should fail")
	}
	// Agents check from inside customer networks, so they must not reach private addresses.
	m.Keyword = ""
	if r := Check(context.Background(), m, PublicDialer()); r.OK || !strings.Contains(r.Error, "private") {
		t.Fatalf("public dialer reached loopback: %+v", r)
	}
}
