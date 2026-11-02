// Public status pages: the page, incident and history pages, subscriptions, Atom/RSS feeds, an
// Atlassian Statuspage–compatible JSON API, a badge, and custom domains.
package server

import (
	"context"
	"embed"
	"encoding/xml"
	"fmt"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed statuspage
var statusFS embed.FS

var statusTmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"label":   func(s string) string { return statusLabels[s] },
	"when":    func(t any) string { return fmtTime(t, "Jan 2, 2006 · 15:04 UTC") },
	"iso":     func(t any) string { return fmtTime(t, time.RFC3339) },
	"day":     func(t any) string { return fmtTime(t, "Jan 2, 2006") },
	"month":   func(t any) string { return fmtTime(t, "January 2006") },
	"pct":     func(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) },
	"safeCSS": func(s string) template.CSS { return template.CSS(s) }, // only for brand_color, which the schema limits to #rrggbb
}).ParseFS(statusFS, "statuspage/*.html"))

func fmtTime(t any, layout string) string {
	switch v := t.(type) {
	case time.Time:
		return v.UTC().Format(layout)
	case *time.Time:
		if v != nil {
			return v.UTC().Format(layout)
		}
	}
	return ""
}

type spPage struct {
	ID, ProjectID, Name, Slug, Description, SupportURL, Color string
	Domain                                                    *string
	HasLogo, Public, EmailEnabled                             bool
}

type spDay struct {
	Date                     time.Time
	Major, Partial, Degraded int // minutes
	NoData                   bool
	Incidents                []string
	Class, Tip               string
}

type spComponent struct {
	ID, Name, Description, Status, Group string
	Days                                 []spDay
	Uptime                               float64
	HasData                              bool
}

type spGroup struct {
	Name, Status string
	Components   []spComponent
}

type spUpdate struct {
	Status, Message string
	At              time.Time
}

type spIncident struct {
	ID, Kind, Name, Impact, Status string
	CreatedAt                      time.Time
	ResolvedAt                     *time.Time
	ScheduledFor, ScheduledUntil   *time.Time
	Updates                        []spUpdate
	Components                     []string
}

type spView struct {
	Page                spPage
	Base                string // "" on a custom domain, else /status/<slug>
	Indicator, Overall  string
	Groups              []spGroup
	Active, Maintenance []spIncident
	Past                []spDate
	Incident            *spIncident
	Months              []spMonth
	Older, Newer        int // history paging; -1 = none
	Message, Title      string
	Token               string
	Now                 time.Time
}

type spDate struct {
	Day       time.Time
	Incidents []spIncident
}

type spMonth struct {
	Month     time.Time
	Incidents []spIncident
}

type ctxBaseKey struct{}

// customDomains serves a status page at the root of its own domain by rewriting /x to /status/<slug>/x.
func (s *Server) customDomains(next http.Handler) http.Handler {
	appHost := ""
	if u, err := url.Parse(s.cfg.AppURL); err == nil {
		appHost = u.Hostname()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		host = strings.ToLower(host)
		if host == appHost || net.ParseIP(host) != nil || host == "localhost" || strings.HasPrefix(r.URL.Path, "/status-assets/") {
			next.ServeHTTP(w, r)
			return
		}
		var slug string
		if s.db.QueryRow(r.Context(), `select slug from status_pages where domain = $1`, host).Scan(&slug) != nil {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(context.WithValue(r.Context(), ctxBaseKey{}, ""))
		r2.URL.Path = strings.TrimSuffix("/status/"+slug+r.URL.Path, "/")
		next.ServeHTTP(w, r2)
	})
}

// tlsAsk lets Caddy's on-demand TLS issue certificates only for configured status page domains.
func (s *Server) tlsAsk(w http.ResponseWriter, r *http.Request) {
	var ok bool
	s.db.QueryRow(r.Context(), `select exists(select 1 from status_pages where domain = $1)`, strings.ToLower(r.URL.Query().Get("domain"))).Scan(&ok)
	if !ok {
		http.NotFound(w, r)
	}
}

// loadPage finds the page for {slug} and checks it may be shown to this visitor.
func (s *Server) loadPage(w http.ResponseWriter, r *http.Request) (*spView, bool) {
	v := &spView{Now: time.Now().UTC(), Older: -1, Newer: -1}
	err := s.db.QueryRow(r.Context(), `select id::text, project_id::text, name, slug, description, support_url, brand_color, domain, logo is not null, public
		from status_pages where slug = $1`, r.PathValue("slug")).Scan(&v.Page.ID, &v.Page.ProjectID, &v.Page.Name, &v.Page.Slug,
		&v.Page.Description, &v.Page.SupportURL, &v.Page.Color, &v.Page.Domain, &v.Page.HasLogo, &v.Page.Public)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	v.Page.EmailEnabled = s.cfg.SMTPHost != "" && s.cfg.SMTPFrom != ""
	if b, ok := r.Context().Value(ctxBaseKey{}).(string); ok {
		v.Base = b
	} else {
		v.Base = "/status/" + v.Page.Slug
	}
	if !v.Page.Public && !s.isOrgMember(r, v.Page.ProjectID) {
		if v.Base == "" { // session cookies belong to Orbit's own domain
			http.NotFound(w, r)
		} else {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
		}
		return nil, false
	}
	if !v.Page.Public {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	return v, true
}

// isOrgMember: is the visitor signed in to Orbit as a member of the project's organization?
func (s *Server) isOrgMember(r *http.Request, projectID string) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	var ok bool
	s.db.QueryRow(r.Context(), `select exists(select 1 from sessions se join projects p on p.id::text = $2
		join memberships m on m.org_id = p.org_id and m.user_id = se.user_id
		where se.token_hash = $1 and se.expires_at > now() and not mfa_blocked(p.org_id, se.user_id))`, hashToken(c.Value), projectID).Scan(&ok)
	return ok
}

func (s *Server) renderStatus(w http.ResponseWriter, name string, v *spView, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := statusTmpl.ExecuteTemplate(w, name, v); err != nil {
		s.log.Error("render status page", "template", name, "err", err)
	}
}

// incidents loads a page's incidents matching where (on i), newest first, with updates and components.
func (s *Server) incidents(ctx context.Context, pageID, where string, args ...any) ([]spIncident, error) {
	rows, err := s.db.Query(ctx, `select i.id::text, i.kind, i.name, i.impact, i.status, i.created_at, i.resolved_at, i.scheduled_for, i.scheduled_until,
		coalesce((select array_agg(c.name order by c.position, c.name) from status_incident_components ic join status_components c on c.id = ic.component_id
		  where ic.incident_id = i.id), '{}')
		from status_incidents i where i.page_id::text = $1 and `+where+` order by coalesce(i.scheduled_for, i.created_at) desc limit 200`, append([]any{pageID}, args...)...)
	if err != nil {
		return nil, err
	}
	var out []spIncident
	idx := map[string]int{}
	for rows.Next() {
		var x spIncident
		if err := rows.Scan(&x.ID, &x.Kind, &x.Name, &x.Impact, &x.Status, &x.CreatedAt, &x.ResolvedAt, &x.ScheduledFor, &x.ScheduledUntil, &x.Components); err != nil {
			rows.Close()
			return nil, err
		}
		idx[x.ID] = len(out)
		out = append(out, x)
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(out))
	for id := range idx {
		ids = append(ids, id)
	}
	rows, err = s.db.Query(ctx, `select incident_id::text, status, message, created_at from status_incident_updates
		where incident_id::text = any($1) order by created_at desc`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var u spUpdate
		rows.Scan(&id, &u.Status, &u.Message, &u.At)
		out[idx[id]].Updates = append(out[idx[id]].Updates, u)
	}
	return out, nil
}

// components loads components with their 90 days of status history, grouped as on the page.
func (s *Server) components(ctx context.Context, pageID string) ([]spGroup, error) {
	// Groups sit where their first component is; components inside a group keep their own order.
	rows, err := s.db.Query(ctx, `select id::text, name, description, status, group_name, created_at from status_components
		where page_id::text = $1
		order by case when group_name = '' then position else min(position) over (partition by group_name) end, group_name, position, name`, pageID)
	if err != nil {
		return nil, err
	}
	var comps []spComponent
	created := map[string]time.Time{}
	idx := map[string]int{}
	for rows.Next() {
		var c spComponent
		var at time.Time
		rows.Scan(&c.ID, &c.Name, &c.Description, &c.Status, &c.Group, &at)
		created[c.ID], idx[c.ID] = at, len(comps)
		comps = append(comps, c)
	}
	rows.Close()

	rows, err = s.db.Query(ctx, `with days as (select generate_series(date_trunc('day', now(), 'UTC') - interval '89 days',
		  date_trunc('day', now(), 'UTC'), interval '1 day') d)
		select c.id::text, d.d, d.d + interval '1 day' <= c.created_at,
		  ceil(coalesce(sum(extract(epoch from least(coalesce(h.ended_at, now()), d.d + interval '1 day') - greatest(h.started_at, d.d)))
		    filter (where h.status = 'major_outage'), 0) / 60)::int,
		  ceil(coalesce(sum(extract(epoch from least(coalesce(h.ended_at, now()), d.d + interval '1 day') - greatest(h.started_at, d.d)))
		    filter (where h.status = 'partial_outage'), 0) / 60)::int,
		  ceil(coalesce(sum(extract(epoch from least(coalesce(h.ended_at, now()), d.d + interval '1 day') - greatest(h.started_at, d.d)))
		    filter (where h.status = 'degraded_performance'), 0) / 60)::int
		from status_components c cross join days d
		left join status_component_history h on h.component_id = c.id and h.started_at < d.d + interval '1 day' and coalesce(h.ended_at, now()) > d.d
		where c.page_id::text = $1 group by c.id, d.d order by c.id, d.d`, pageID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var d spDay
		rows.Scan(&id, &d.Date, &d.NoData, &d.Major, &d.Partial, &d.Degraded) // minutes, rounded up so a short outage still shows
		c := &comps[idx[id]]
		c.Days = append(c.Days, d)
	}
	rows.Close()

	names := map[string][]string{} // component id + day → incident names
	rows, err = s.db.Query(ctx, `select ic.component_id::text, date_trunc('day', i.created_at, 'UTC'), i.name
		from status_incidents i join status_incident_components ic on ic.incident_id = i.id
		where i.page_id::text = $1 and i.kind = 'incident' and i.created_at > now() - interval '91 days'`, pageID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name string
		var day time.Time
		rows.Scan(&id, &day, &name)
		k := id + day.UTC().Format("2006-01-02")
		names[k] = append(names[k], name)
	}
	rows.Close()

	now := time.Now()
	for i := range comps {
		c := &comps[i]
		var down float64
		for j := range c.Days {
			d := &c.Days[j]
			d.Incidents = names[c.ID+d.Date.UTC().Format("2006-01-02")]
			d.Class, d.Tip = barStyle(*d)
			down += float64(d.Major) + 0.3*float64(d.Partial) // Statuspage weights partial outages at 30%
		}
		start := created[c.ID]
		if from := now.Add(-90 * 24 * time.Hour); start.Before(from) {
			start = from
		}
		if total := now.Sub(start).Minutes(); total > 1 {
			// Round down, so any downtime shows as below 100.00 %.
			c.Uptime, c.HasData = max(0, math.Floor(10000*(1-down/total))/100), true
		}
	}

	var groups []spGroup
	for _, c := range comps {
		if n := len(groups); n == 0 || c.Group == "" || groups[n-1].Name != c.Group {
			groups = append(groups, spGroup{Name: c.Group, Status: c.Status})
		}
		g := &groups[len(groups)-1]
		g.Components = append(g.Components, c)
		g.Status = worse(g.Status, c.Status)
	}
	return groups, nil
}

func worse(a, b string) string {
	rank := func(s string) int {
		for i, x := range componentStatuses {
			if x == s {
				return i
			}
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func barStyle(d spDay) (class, tip string) {
	mins := func(m int) string {
		if m >= 60 {
			return fmt.Sprintf("%d hr %d mins", m/60, m%60)
		}
		return fmt.Sprintf("%d mins", m)
	}
	var parts []string
	if d.Major > 0 {
		parts = append(parts, "Major outage "+mins(d.Major))
	}
	if d.Partial > 0 {
		parts = append(parts, "Partial outage "+mins(d.Partial))
	}
	if d.Degraded > 0 {
		parts = append(parts, "Degraded performance "+mins(d.Degraded))
	}
	switch {
	case d.NoData:
		return "nodata", "No data exists for this day."
	case d.Major > 0:
		class = "major"
	case d.Partial > 0:
		class = "partial"
	case d.Degraded > 0:
		class = "degraded"
	case len(d.Incidents) > 0:
		class = "minor-incident"
	default:
		class = "ok"
	}
	if len(parts) == 0 {
		parts = []string{"No downtime recorded on this day."}
	}
	return class, strings.Join(parts, " · ")
}

// overall is the page-level indicator and description, as Statuspage's status.json reports them.
func overall(groups []spGroup, active []spIncident) (string, string) {
	rank := map[string]int{"none": 0, "maintenance": 1, "minor": 2, "major": 3, "critical": 4}
	ind := "none"
	up := func(x string) {
		if rank[x] > rank[ind] {
			ind = x
		}
	}
	for _, i := range active {
		if i.Kind == "incident" {
			up(i.Impact)
		}
	}
	for _, g := range groups {
		for _, c := range g.Components {
			up(map[string]string{"degraded_performance": "minor", "partial_outage": "major", "major_outage": "critical",
				"under_maintenance": "maintenance", "operational": "none"}[c.Status])
		}
	}
	return ind, map[string]string{"none": "All Systems Operational", "minor": "Minor Service Outage", "major": "Partial System Outage",
		"critical": "Major System Outage", "maintenance": "Service Under Maintenance"}[ind]
}

func (s *Server) publicPage(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var err error
	if v.Groups, err = s.components(ctx, v.Page.ID); err != nil {
		s.statusError(w, err)
		return
	}
	if v.Active, err = s.incidents(ctx, v.Page.ID, `i.kind = 'incident' and i.status <> 'resolved'`); err != nil {
		s.statusError(w, err)
		return
	}
	if v.Maintenance, err = s.incidents(ctx, v.Page.ID, `i.kind = 'maintenance' and i.status in ('scheduled','in_progress','verifying')`); err != nil {
		s.statusError(w, err)
		return
	}
	v.Indicator, v.Overall = overall(v.Groups, v.Active)
	past, err := s.incidents(ctx, v.Page.ID, `coalesce(i.scheduled_for, i.created_at) > date_trunc('day', now(), 'UTC') - interval '6 days'
		and coalesce(i.scheduled_for, i.created_at) <= now()`)
	if err != nil {
		s.statusError(w, err)
		return
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for d := 0; d < 7; d++ {
		day := spDate{Day: today.AddDate(0, 0, -d)}
		for _, i := range past {
			at := i.CreatedAt
			if i.ScheduledFor != nil {
				at = *i.ScheduledFor
			}
			if at.UTC().Truncate(24 * time.Hour).Equal(day.Day) {
				day.Incidents = append(day.Incidents, i)
			}
		}
		v.Past = append(v.Past, day)
	}
	w.Header().Set("Cache-Control", "no-cache")
	s.renderStatus(w, "page", v, 200)
}

func (s *Server) statusError(w http.ResponseWriter, err error) {
	s.log.Error("status page", "err", err)
	http.Error(w, "Something went wrong loading this status page.", 500)
}

func (s *Server) publicIncident(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	list, err := s.incidents(r.Context(), v.Page.ID, `i.id::text = $2`, r.PathValue("iid"))
	if err != nil || len(list) == 0 {
		v.Title, v.Message = "Incident not found", "This incident doesn't exist or was removed."
		s.renderStatus(w, "message", v, 404)
		return
	}
	v.Incident = &list[0]
	s.renderStatus(w, "incident", v, 200)
}

// publicHistory lists incidents three months at a time.
func (s *Server) publicHistory(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	n = max(0, min(n, 120))
	first := time.Date(v.Now.Year(), v.Now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -3*n, 0)
	from := first.AddDate(0, -2, 0)
	list, err := s.incidents(r.Context(), v.Page.ID, `coalesce(i.scheduled_for, i.created_at) >= $2 and coalesce(i.scheduled_for, i.created_at) < $3
		and coalesce(i.scheduled_for, i.created_at) <= now()`, from, first.AddDate(0, 1, 0))
	if err != nil {
		s.statusError(w, err)
		return
	}
	for m := 0; m < 3; m++ {
		month := spMonth{Month: first.AddDate(0, -m, 0)}
		for _, i := range list {
			at := i.CreatedAt
			if i.ScheduledFor != nil {
				at = *i.ScheduledFor
			}
			if at.Year() == month.Month.Year() && at.Month() == month.Month.Month() {
				month.Incidents = append(month.Incidents, i)
			}
		}
		v.Months = append(v.Months, month)
	}
	v.Older = n + 1
	if n > 0 {
		v.Newer = n - 1
	}
	s.renderStatus(w, "history", v, 200)
}

func (s *Server) publicSubscribe(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	if r.Method == "GET" {
		s.renderStatus(w, "subscribe", v, 200)
		return
	}
	key := "subscribe:" + clientIP(r)
	if s.attempts.blocked(key, 10) {
		v.Title, v.Message = "Too many requests", "Please try again in 15 minutes."
		s.renderStatus(w, "message", v, 429)
		return
	}
	s.attempts.fail(key)
	r.ParseForm()
	email, hook := strings.ToLower(strings.TrimSpace(r.PostForm.Get("email"))), strings.TrimSpace(r.PostForm.Get("webhook"))
	base := s.statusPageURL(v.Page.Slug, v.Page.Domain)
	switch {
	case email != "" && v.Page.EmailEnabled:
		if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(email, "\r\n") {
			v.Title, v.Message = "Check your email address", "That doesn't look like a valid email address."
			s.renderStatus(w, "message", v, 400)
			return
		}
		var token string
		var confirmed bool
		err := s.db.QueryRow(r.Context(), `insert into status_subscribers (page_id, email, token) values ($1,$2,$3)
			on conflict (page_id, email) do update set email = excluded.email returning token, confirmed_at is not null`,
			v.Page.ID, email, randomSecret()).Scan(&token, &confirmed)
		if err != nil {
			s.statusError(w, err)
			return
		}
		if !confirmed {
			go func() {
				if err := s.sendTemplate([]string{email}, "Confirm your subscription to "+v.Page.Name, "status-confirm",
					map[string]string{"PageName": v.Page.Name, "Link": base + "/confirm/" + token}); err != nil {
					s.log.Warn("status confirm email failed", "err", err)
				}
			}()
		}
		// The same answer either way, so the form can't be used to learn who is subscribed.
		v.Title, v.Message = "Check your inbox", "If "+email+" isn't subscribed yet, we've sent it a link to confirm."
	case hook != "":
		u, err := url.Parse(hook)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			v.Title, v.Message = "Check the webhook address", "Enter a full http:// or https:// URL."
			s.renderStatus(w, "message", v, 400)
			return
		}
		s.db.Exec(r.Context(), `insert into status_subscribers (page_id, webhook_url, token, confirmed_at) values ($1,$2,$3,now())
			on conflict do nothing`, v.Page.ID, hook, randomSecret())
		v.Title, v.Message = "Webhook subscribed", "We'll POST a JSON message to it whenever an incident is created or updated."
	default:
		v.Title, v.Message = "Nothing to subscribe", "Enter an email address or a webhook URL."
		s.renderStatus(w, "message", v, 400)
		return
	}
	s.renderStatus(w, "message", v, 200)
}

func (s *Server) publicConfirm(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	tag, _ := s.db.Exec(r.Context(), `update status_subscribers set confirmed_at = coalesce(confirmed_at, now()) where page_id::text = $1 and token = $2`,
		v.Page.ID, r.PathValue("token"))
	if tag.RowsAffected() == 0 {
		v.Title, v.Message = "Link expired", "This confirmation link isn't valid any more. Subscribe again to get a new one."
		s.renderStatus(w, "message", v, 404)
		return
	}
	v.Title, v.Message = "You're subscribed", "You'll get an email whenever "+v.Page.Name+" creates or updates an incident or maintenance."
	s.renderStatus(w, "message", v, 200)
}

// publicUnsubscribe asks for a click before removing, so link scanners in mail clients don't unsubscribe people.
func (s *Server) publicUnsubscribe(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	if r.Method == "GET" {
		v.Token = r.PathValue("token")
		s.renderStatus(w, "unsubscribe", v, 200)
		return
	}
	s.db.Exec(r.Context(), `delete from status_subscribers where page_id::text = $1 and token = $2`, v.Page.ID, r.PathValue("token"))
	v.Title, v.Message = "Unsubscribed", "You won't get any more updates from "+v.Page.Name+"."
	s.renderStatus(w, "message", v, 200)
}

func (s *Server) publicLogo(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	var b []byte
	var typ string
	if s.db.QueryRow(r.Context(), `select logo, logo_type from status_pages where id::text = $1 and logo is not null`, v.Page.ID).Scan(&b, &typ) != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(b)
}

// publicFeed serves the incident history as Atom or RSS.
func (s *Server) publicFeed(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	list, err := s.incidents(r.Context(), v.Page.ID, `coalesce(i.scheduled_for, i.created_at) <= now() + interval '30 days'`)
	if err != nil {
		s.statusError(w, err)
		return
	}
	base := s.statusPageURL(v.Page.Slug, v.Page.Domain)
	body := func(i spIncident) string {
		var b strings.Builder
		for _, u := range i.Updates {
			fmt.Fprintf(&b, "<p><small>%s</small><br><strong>%s</strong> - %s</p>", u.At.UTC().Format(time.RFC1123),
				template.HTMLEscapeString(statusLabels[u.Status]), template.HTMLEscapeString(u.Message))
		}
		return b.String()
	}
	updated := func(i spIncident) time.Time {
		if len(i.Updates) > 0 {
			return i.Updates[0].At
		}
		return i.CreatedAt
	}
	if len(list) > 50 {
		list = list[:50]
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	if strings.HasSuffix(r.URL.Path, ".rss") {
		type item struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			GUID        string `xml:"guid"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
		}
		feed := struct {
			XMLName xml.Name `xml:"rss"`
			Version string   `xml:"version,attr"`
			Channel struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				Items       []item `xml:"item"`
			} `xml:"channel"`
		}{Version: "2.0"}
		feed.Channel.Title, feed.Channel.Link, feed.Channel.Description = v.Page.Name+" status", base, "Incident history for "+v.Page.Name
		for _, i := range list {
			feed.Channel.Items = append(feed.Channel.Items, item{i.Name, base + "/incidents/" + i.ID, base + "/incidents/" + i.ID,
				updated(i).UTC().Format(time.RFC1123Z), body(i)})
		}
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		w.Write([]byte(xml.Header))
		xml.NewEncoder(w).Encode(feed)
		return
	}
	type link struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr,omitempty"`
	}
	type entry struct {
		ID      string `xml:"id"`
		Title   string `xml:"title"`
		Updated string `xml:"updated"`
		Link    link   `xml:"link"`
		Content struct {
			Type string `xml:"type,attr"`
			Body string `xml:",chardata"`
		} `xml:"content"`
	}
	feed := struct {
		XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
		ID      string   `xml:"id"`
		Title   string   `xml:"title"`
		Updated string   `xml:"updated"`
		Links   []link   `xml:"link"`
		Entries []entry  `xml:"entry"`
	}{ID: base, Title: v.Page.Name + " status", Updated: v.Now.Format(time.RFC3339),
		Links: []link{{Href: base}, {Href: base + "/history.atom", Rel: "self"}}}
	for _, i := range list {
		e := entry{ID: base + "/incidents/" + i.ID, Title: i.Name, Updated: updated(i).UTC().Format(time.RFC3339), Link: link{Href: base + "/incidents/" + i.ID}}
		e.Content.Type, e.Content.Body = "html", body(i)
		feed.Entries = append(feed.Entries, e)
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(feed)
}

// publicJSON serves summary.json and status.json in Atlassian Statuspage's v2 shape, so existing integrations work.
func (s *Server) publicJSON(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	groups, err := s.components(r.Context(), v.Page.ID)
	if err != nil {
		s.statusError(w, err)
		return
	}
	open, err := s.incidents(r.Context(), v.Page.ID, `i.status not in ('resolved','completed')`)
	if err != nil {
		s.statusError(w, err)
		return
	}
	var active []spIncident
	var maint []map[string]any
	inc := func(i spIncident) map[string]any {
		var ups []map[string]any
		for _, u := range i.Updates {
			ups = append(ups, map[string]any{"status": u.Status, "body": u.Message, "created_at": u.At})
		}
		return map[string]any{"id": i.ID, "name": i.Name, "status": i.Status, "impact": i.Impact, "created_at": i.CreatedAt,
			"scheduled_for": i.ScheduledFor, "scheduled_until": i.ScheduledUntil, "incident_updates": ups, "components": i.Components,
			"shortlink": s.statusPageURL(v.Page.Slug, v.Page.Domain) + "/incidents/" + i.ID}
	}
	incs := []map[string]any{}
	for _, i := range open {
		if i.Kind == "incident" {
			active = append(active, i)
			incs = append(incs, inc(i))
		} else {
			maint = append(maint, inc(i))
		}
	}
	ind, desc := overall(groups, active)
	out := map[string]any{
		"page":   map[string]any{"id": v.Page.ID, "name": v.Page.Name, "url": s.statusPageURL(v.Page.Slug, v.Page.Domain), "time_zone": "Etc/UTC", "updated_at": v.Now},
		"status": map[string]string{"indicator": ind, "description": desc},
	}
	if strings.HasSuffix(r.URL.Path, "summary.json") {
		comps := []map[string]any{}
		for _, g := range groups {
			for i, c := range g.Components {
				comps = append(comps, map[string]any{"id": c.ID, "name": c.Name, "description": c.Description, "status": c.Status,
					"group_name": nilIfEmpty(g.Name), "position": i + 1})
			}
		}
		if maint == nil {
			maint = []map[string]any{}
		}
		out["components"], out["incidents"], out["scheduled_maintenances"] = comps, incs, maint
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, 200, out)
}

// publicBadge is a shields-style SVG for READMEs.
func (s *Server) publicBadge(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadPage(w, r)
	if !ok {
		return
	}
	groups, _ := s.components(r.Context(), v.Page.ID)
	active, _ := s.incidents(r.Context(), v.Page.ID, `i.kind = 'incident' and i.status <> 'resolved'`)
	ind, desc := overall(groups, active)
	color := map[string]string{"none": "#2fcc66", "minor": "#e3a008", "major": "#e67e22", "critical": "#e74c3c", "maintenance": "#3498db"}[ind]
	text := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(desc, "All Systems "), "Service "))
	lw, rw := 46, 12+7*len(text)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=60")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%[1]d" height="20" role="img" aria-label="status: %[4]s">`+
		`<rect width="%[2]d" height="20" fill="#555"/><rect x="%[2]d" width="%[3]d" height="20" fill="%[5]s"/>`+
		`<g fill="#fff" font-family="Verdana,DejaVu Sans,sans-serif" font-size="11" text-anchor="middle">`+
		`<text x="%[6]d" y="14">status</text><text x="%[7]d" y="14">%[4]s</text></g></svg>`,
		lw+rw, lw, rw, template.HTMLEscapeString(text), color, lw/2, lw+rw/2)
}

// localTimeJS rewrites <time> elements into the visitor's time zone. Pages work without it (times show in UTC).
const localTimeJS = `document.querySelectorAll('time[datetime]').forEach(function (el) {
  var d = new Date(el.getAttribute('datetime')); if (isNaN(d)) return;
  var o = el.dataset.format === 'day' ? { month: 'short', day: 'numeric', year: 'numeric' }
    : { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short' };
  el.textContent = d.toLocaleString([], o);
});`

func serveLocalTimeJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(localTimeJS))
}
