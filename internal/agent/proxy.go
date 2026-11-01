package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"orbit/internal/protocol"
)

// proxy is the zero-downtime router for apps deployed in proxy mode. It owns the app's public port and
// forwards to whichever container is current, switching atomically. It also measures requests.
// shortcut: HTTP/1.1 + h2c-less only, no TLS termination; put a TLS-terminating LB in front for HTTPS.
type proxy struct {
	mu     sync.Mutex
	log    *slog.Logger
	routes map[string]*route // app → route
}

type route struct {
	listen   string
	upstream atomic.Pointer[url.URL]
	srv      *http.Server

	mu       sync.Mutex
	requests int
	errors   int
	durs     []float64
}

func newProxy(log *slog.Logger) *proxy { return &proxy{log: log, routes: map[string]*route{}} }

func (p *proxy) route(app, listen, upstream string) error {
	target := &url.URL{Scheme: "http", Host: upstream}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.routes[app]; r != nil {
		if r.listen == listen {
			r.upstream.Store(target)
			return nil
		}
		r.srv.Close()
		delete(p.routes, app)
	}
	ln, err := net.Listen("tcp", ":"+listen)
	if err != nil {
		return err
	}
	r := &route{listen: listen}
	r.upstream.Store(target)
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(r.upstream.Load())
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	r.srv = &http.Server{
		ReadHeaderTimeout: 30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			rp.ServeHTTP(sw, req)
			r.record(sw.status, time.Since(start))
		}),
	}
	go func() {
		if err := r.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.log.Error("proxy stopped", "app", app, "err", err)
		}
	}()
	p.routes[app] = r
	p.log.Info("proxy route", "app", app, "listen", listen, "upstream", upstream)
	return nil
}

func (p *proxy) remove(app string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.routes[app]; r != nil {
		r.srv.Close()
		delete(p.routes, app)
	}
}

func (r *route) record(status int, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests++
	if status >= 500 {
		r.errors++
	}
	if len(r.durs) < 20000 {
		r.durs = append(r.durs, float64(d.Microseconds())/1000)
	}
}

// snapshot returns and resets per-app request counters.
func (p *proxy) snapshot() []protocol.AppMetric {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []protocol.AppMetric{}
	for app, r := range p.routes {
		r.mu.Lock()
		m := protocol.AppMetric{App: app, Requests: r.requests, Errors: r.errors}
		if n := len(r.durs); n > 0 {
			sort.Float64s(r.durs)
			var sum float64
			for _, d := range r.durs {
				sum += d
			}
			m.AvgMs, m.P95Ms = sum/float64(n), r.durs[min(n-1, n*95/100)]
		}
		r.requests, r.errors, r.durs = 0, 0, r.durs[:0]
		r.mu.Unlock()
		out = append(out, m)
	}
	return out
}

// restore rebuilds routes from running containers' labels, so an agent restart keeps serving traffic.
func (p *proxy) restore(ctx context.Context, d *Docker) {
	cs, err := d.list(ctx, `{"label":["orbit.proxy.listen"],"status":["running"]}`)
	if err != nil {
		return
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Created > cs[j].Created })
	seen := map[string]bool{}
	for _, c := range cs {
		app := c.Labels["orbit.app"]
		if app == "" || seen[app] {
			continue
		}
		seen[app] = true
		if ip := c.ip(); ip != "" {
			if err := p.route(app, c.Labels["orbit.proxy.listen"], ip+":"+strings.Split(c.Labels["orbit.proxy.target"], "/")[0]); err != nil {
				p.log.Error("restore proxy route", "app", app, "err", err)
			}
		}
	}
}

func (a *Agent) reportAppMetrics(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if apps := a.proxy.snapshot(); len(apps) > 0 {
				a.sendNow(ctx, protocol.TypeAppMetrics, protocol.AppMetricsBatch{TS: time.Now().UTC(), Apps: apps})
			}
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int)        { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
