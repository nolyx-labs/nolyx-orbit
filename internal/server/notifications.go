// Notification channels and delivery (email, Slack, Discord, webhook).
package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/textproto"
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
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: s.safeDialer().DialContext}}
}

// safeDialer is the dialer behind safeClient, also used by uptime monitors.
func (s *Server) safeDialer() *net.Dialer {
	return &net.Dialer{
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
	}
}

func (s *Server) notify(ctx context.Context, a alertState, status string) {
	text := "[Nolyx Orbit] " + a.describe(status)
	at := time.Now().UTC()
	payload := map[string]any{"status": status, "rule": a.Rule, "metric": a.Metric, "threshold": a.Threshold, "value": a.Value,
		"target_type": a.TargetType, "target_id": a.TargetID, "target": a.TargetName, "at": at, "text": text, "content": text}
	mail := s.alertEmail(a, status, at)
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
		if err := s.deliver(ctx, c.typ, c.cfg, text, payload, mail); err != nil {
			s.log.Warn("notification failed", "channel", c.name, "err", err)
			s.event(ctx, a.ProjectID, "", "warning", fmt.Sprintf("Notification channel %s failed: %v", c.name, err))
		}
	}
}

// alertEmail is the data for emails/alert.html.
func (s *Server) alertEmail(a alertState, status string, at time.Time) mailMsg {
	label, color := "Resolved", "#16a34a"
	if status == "firing" {
		label, color = "Firing", "#dc2626"
	}
	value := "—"
	if unit := alertMetrics[a.Metric].unit; unit != "" {
		value = fmt.Sprintf("%.1f%s (threshold %.1f%s)", a.Value, unit, a.Threshold, unit)
	}
	return mailMsg{"alert", map[string]string{
		"Status": label, "Color": color, "Title": fmt.Sprintf("%s is %s on %s", a.Rule, status, a.TargetName),
		"Rule": a.Rule + " (" + a.Metric + ")", "Target": a.TargetType + " " + a.TargetName, "Value": value,
		"Time": at.Format("2 Jan 2006, 15:04 UTC"), "Link": s.cfg.AppURL + "/p/" + a.ProjectID + "/alerts",
	}}
}

// mailMsg is what an email channel sends: a template from emails/ and its data.
type mailMsg struct {
	template string
	data     map[string]string
}

func (s *Server) deliver(ctx context.Context, typ string, cfg channelConfig, text string, payload map[string]any, mail mailMsg) error {
	switch typ {
	case "slack":
		return s.postJSON(ctx, cfg.URL, map[string]string{"text": text})
	case "discord":
		return s.postJSON(ctx, cfg.URL, map[string]string{"content": text})
	case "webhook":
		return s.postJSON(ctx, cfg.URL, payload)
	case "email":
		return s.sendTemplate(cfg.To, text, mail.template, mail.data)
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

// sendEmail sends a multipart/alternative message: plain text for simple clients, HTML for the rest.
func (s *Server) sendEmail(to []string, subject, text, html string) error {
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		return errNoSMTP
	}
	for _, addr := range to {
		if strings.ContainsAny(addr, "\r\n") {
			return errors.New("invalid recipient")
		}
	}
	// multipart/alternative: plain text, then multipart/related holding the HTML and the logo it shows as cid:logo.
	qp := func(mw *multipart.Writer, typ, content string) {
		w, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {typ + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		q := quotedprintable.NewWriter(w) // keeps lines under SMTP's 998-character limit
		q.Write([]byte(content))
		q.Close()
	}
	var related bytes.Buffer
	rw := multipart.NewWriter(&related)
	qp(rw, "text/html", html)
	if strings.Contains(html, "cid:logo") { // an unreferenced inline image shows up as an attachment in some clients
		w, _ := rw.CreatePart(textproto.MIMEHeader{"Content-Type": {"image/png"}, "Content-ID": {"<logo>"},
			"Content-Disposition": {`inline; filename="logo.png"`}, "Content-Transfer-Encoding": {"base64"}})
		b64 := base64.StdEncoding.EncodeToString(emailLogo)
		for len(b64) > 76 {
			io.WriteString(w, b64[:76]+"\r\n")
			b64 = b64[76:]
		}
		io.WriteString(w, b64)
	}
	rw.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	qp(mw, "text/plain", text)
	w, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"multipart/related; boundary=" + rw.Boundary()}})
	w.Write(related.Bytes())
	mw.Close()
	msg := "From: " + s.cfg.SMTPFrom + "\r\nTo: " + strings.Join(to, ", ") +
		"\r\nSubject: " + mime.QEncoding.Encode("utf-8", strings.NewReplacer("\r", "", "\n", " ").Replace(subject)) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=" + mw.Boundary() + "\r\n\r\n" + body.String()
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
		mail := mailMsg{"test-notification", map[string]string{"Channel": in.Name}}
		if err := s.deliver(r.Context(), in.Type, cfg, text, map[string]any{"status": "test", "text": text, "content": text}, mail); err != nil {
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
