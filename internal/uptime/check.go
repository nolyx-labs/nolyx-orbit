// Package uptime runs one HTTP or TCP check. The control plane and agents (as extra check locations) share it.
package uptime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"orbit/internal/protocol"
)

// Check runs m once through dialer, which decides which destinations are allowed.
func Check(ctx context.Context, m protocol.MonitorConfig, dialer *net.Dialer) protocol.MonitorResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutMS)*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := protocol.MonitorResult{MonitorID: m.ID, TS: start.UTC()}
	done := func(err error) protocol.MonitorResult {
		res.LatencyMS = int(time.Since(start).Milliseconds())
		if err != nil {
			res.Error = err.Error()
			if len(res.Error) > 300 {
				res.Error = res.Error[:300]
			}
		} else {
			res.OK = true
		}
		return res
	}
	if m.Type == "tcp" {
		conn, err := dialer.DialContext(ctx, "tcp", m.Target)
		if err == nil {
			conn.Close()
		}
		return done(err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", m.Target, nil)
	if err != nil {
		return done(err)
	}
	req.Header.Set("User-Agent", "NolyxOrbit-Monitor/1")
	client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{}, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return done(err)
	}
	defer resp.Body.Close()
	res.StatusCode = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		exp := resp.TLS.PeerCertificates[0].NotAfter
		res.CertExpiry = &exp
	}
	if !StatusMatches(m.ExpectedStatus, resp.StatusCode) {
		return done(fmt.Errorf("unexpected status %d (expected %s)", resp.StatusCode, m.ExpectedStatus))
	}
	if m.Keyword != "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if !strings.Contains(string(body), m.Keyword) {
			return done(fmt.Errorf("response doesn't contain %q", m.Keyword))
		}
	}
	return done(nil)
}

// StatusMatches checks an HTTP status against a spec like "2xx", "200" or "200-299,301".
func StatusMatches(spec string, code int) bool {
	for _, p := range strings.Split(spec, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if len(p) == 3 && strings.HasSuffix(p, "xx") {
			if code/100 == int(p[0]-'0') {
				return true
			}
			continue
		}
		lo, hi, isRange := strings.Cut(p, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		if code >= a && code <= b {
			return true
		}
	}
	return false
}

// PublicDialer refuses loopback, private and link-local destinations (checked after DNS resolution).
func PublicDialer() *net.Dialer {
	return &net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		if ip := net.ParseIP(host); ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return errors.New("destination is a private address")
		}
		return nil
	}}
}
