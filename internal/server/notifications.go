// Notification channels and delivery (email, Slack, Discord, webhook).
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"syscall"
	"time"
)

type channelConfig struct {
	URL string   `json:"url,omitempty"`
	To  []string `json:"to,omitempty"`
}

// safeClient refuses private/loopback destinations (SSRF) unless explicitly allowed. It applies to redirects too.
func (s *Server) safeClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: (&net.Dialer{
		Control: func(_, address string, _ syscall.RawConn) error {
			if s.cfg.AllowPrivateWebhooks {
				return nil
			}
			host, _, _ := net.SplitHostPort(address)
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				return errors.New("destination is a private address (set ALLOW_PRIVATE_WEBHOOKS=true to allow)")
			}
			return nil
		},
	}).DialContext}}
}

func (s *Server) notify(ctx context.Context, a alertState, status string) {
	text := "[Nolyx Orbit] " + a.describe(status)
	payload := map[string]any{"status": status, "rule": a.Rule, "metric": a.Metric, "threshold": a.Threshold, "value": a.Value,
		"target_type": a.TargetType, "target_id": a.TargetID, "target": a.TargetName, "at": time.Now().UTC(), "text": text, "content": text}
	if a.Webhook != "" { // legacy per-rule webhook
		if err := s.postJSON(ctx, a.Webhook, payload); err != nil {
			s.log.Warn("alert webhook failed", "rule", a.Rule, "err", err)
		}
	}
	if len(a.Channels) == 0 {
		return
	}
	rows, err := s.db.Query(ctx, `select name, type, config_encrypted from notification_channels where project_id::text = $1 and id::text = any($2)`, a.ProjectID, a.Channels)
	if err != nil {
		return
	}
	type ch struct {
		name, typ string
		cfg       channelConfig
	}
	var chans []ch
	for rows.Next() {
		var c ch
		var enc []byte
		rows.Scan(&c.name, &c.typ, &enc)
		if b, err := s.decrypt(enc); err == nil {
			json.Unmarshal(b, &c.cfg)
			chans = append(chans, c)
		}
	}
	rows.Close()
	for _, c := range chans {
		if err := s.deliver(ctx, c.typ, c.cfg, text, payload); err != nil {
			s.log.Warn("notification failed", "channel", c.name, "err", err)
			s.event(ctx, a.ProjectID, "", "warning", fmt.Sprintf("Notification channel %s failed: %v", c.name, err))
		}
	}
}

func (s *Server) deliver(ctx context.Context, typ string, cfg channelConfig, text string, payload map[string]any) error {
	switch typ {
	case "slack":
		return s.postJSON(ctx, cfg.URL, map[string]string{"text": text})
	case "discord":
		return s.postJSON(ctx, cfg.URL, map[string]string{"content": text})
	case "webhook":
		return s.postJSON(ctx, cfg.URL, payload)
	case "email":
		return s.sendEmail(cfg.To, text, text+"\n\nSent by Nolyx Orbit ("+s.cfg.AppURL+").")
	}
	return fmt.Errorf("unknown channel type %q", typ)
}

func (s *Server) postJSON(ctx context.Context, u string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.safeClient().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

var errNoSMTP = errors.New("email is not configured on this instance (set SMTP_HOST and SMTP_FROM)")

func (s *Server) sendEmail(to []string, subject, body string) error {
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		return errNoSMTP
	}
	for _, addr := range to {
		if strings.ContainsAny(addr, "\r\n") {
			return errors.New("invalid recipient")
		}
	}
	msg := "From: " + s.cfg.SMTPFrom + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + strings.NewReplacer("\r", "", "\n", " ").Replace(subject) +
		"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
	var auth smtp.Auth
	if s.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
	}
	return smtp.SendMail(net.JoinHostPort(s.cfg.SMTPHost, s.cfg.SMTPPort), auth, s.cfg.SMTPFrom, to, []byte(msg))
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select id, name, type, created_at from notification_channels where project_id = $1 order by created_at`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"channels": out, "email_configured": s.cfg.SMTPHost != ""})
	return nil
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in struct {
		Name string   `json:"name"`
		Type string   `json:"type"`
		URL  string   `json:"url"`
		To   []string `json:"to"`
		Test bool     `json:"test"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Name) == "" {
		return errStatus(400, "name is required")
	}
	cfg := channelConfig{}
	switch in.Type {
	case "slack", "discord", "webhook":
		if u, err := url.Parse(in.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errStatus(400, "a valid http(s) URL is required")
		}
		cfg.URL = in.URL
	case "email":
		if len(in.To) == 0 || len(in.To) > 20 {
			return errStatus(400, "1–20 recipients required")
		}
		for _, a := range in.To {
			if !strings.Contains(a, "@") || strings.ContainsAny(a, "\r\n ,") {
				return errStatus(400, "invalid email %q", a)
			}
		}
		cfg.To = in.To
	default:
		return errStatus(400, "type must be email, slack, discord or webhook")
	}
	if in.Test {
		text := "[Nolyx Orbit] Test notification from " + in.Name
		if err := s.deliver(r.Context(), in.Type, cfg, text, map[string]any{"status": "test", "text": text, "content": text}); err != nil {
			return errStatus(400, "test delivery failed: %v", err)
		}
	}
	b, _ := json.Marshal(cfg)
	out, err := s.one(r.Context(), `insert into notification_channels (project_id, name, type, config_encrypted) values ($1,$2,$3,$4) returning id`,
		p.ID, in.Name, in.Type, s.encrypt(b))
	if err != nil {
		return err
	}
	s.auditProject(r, p, "channel.create", in.Name, map[string]any{"type": in.Type})
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from notification_channels where project_id = $1 and id::text = $2 returning name`, p.ID, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	s.db.Exec(r.Context(), `update alert_rules set channel_ids = array_remove(channel_ids, $2::uuid) where project_id = $1`, p.ID, r.PathValue("id"))
	s.auditProject(r, p, "channel.delete", name, nil)
	w.WriteHeader(204)
	return nil
}
