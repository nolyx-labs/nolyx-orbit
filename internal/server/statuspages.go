// Status pages (Atlassian Statuspage style): components, incidents, scheduled maintenance and subscribers.
// This file is the management API and the worker; statuspage_public.go serves the public pages.
package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	componentStatuses = []string{"operational", "under_maintenance", "degraded_performance", "partial_outage", "major_outage"} // by severity
	incidentStatuses  = map[string][]string{
		"incident":    {"investigating", "identified", "monitoring", "resolved"},
		"maintenance": {"scheduled", "in_progress", "verifying", "completed"},
	}
	slugRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)
	colorRe  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// effectiveStatusSQL derives each component's status: worst of its source and open incidents, except that
// in-progress maintenance shows "under maintenance" unless an incident says otherwise.
const effectiveStatusSQL = `
with src as (
  select c.id, coalesce(case c.source_type
    when 'manual' then c.manual_status
    when 'monitor' then (select case when m.status = 'down' and not m.paused then 'major_outage' end from monitors m where m.id = c.source_id)
    when 'server' then (select case when s.status = 'offline' then 'major_outage' end from servers s where s.id = c.source_id)
    when 'app' then (select case when ct.state in ('exited','dead') then 'major_outage' when ct.health = 'unhealthy' then 'partial_outage'
        when ct.state = 'restarting' then 'degraded_performance' end
      from applications a left join lateral (select state, health from containers x where x.server_id = a.server_id
        and x.labels->>'orbit.app' = a.name order by created desc limit 1) ct on true where a.id = c.source_id)
  end, 'operational') status
  from status_components c
), inc as (
  select ic.component_id id, max(array_position($1::text[], ic.status)) rank
  from status_incident_components ic join status_incidents i on i.id = ic.incident_id
  where i.kind = 'incident' and i.status <> 'resolved' group by 1
), maint as (
  select distinct ic.component_id id from status_incident_components ic join status_incidents i on i.id = ic.incident_id
  where i.kind = 'maintenance' and i.status in ('in_progress','verifying')
  union select c.id from status_components c join servers_on_hold h on (c.source_type = 'server' and c.source_id = h.id)
    or (c.source_type = 'monitor' and c.source_id in (select id from monitors where server_id = h.id))
), eff as (
  select src.id, case when inc.id is null and maint.id is not null then 'under_maintenance'
    else ($1::text[])[greatest(array_position($1::text[], src.status), coalesce(inc.rank, 1))] end status
  from src left join inc on inc.id = src.id left join maint on maint.id = src.id
)
update status_components c set status = eff.status from eff where c.id = eff.id and c.status <> eff.status`

// updateStatusPages runs maintenance on schedule, refreshes component statuses and records their history.
func (s *Server) updateStatusPages(ctx context.Context) {
	for _, t := range []struct{ from, to, message string }{
		{"scheduled", "in_progress", "Scheduled maintenance is currently in progress. We will provide updates as necessary."},
		{"in_progress", "completed", "The scheduled maintenance has been completed."},
	} {
		col := "scheduled_for"
		if t.to == "completed" {
			col = "scheduled_until"
		}
		rows, err := s.db.Query(ctx, `update status_incidents set status = $2, resolved_at = case when $2 = 'completed' then now() end
			where kind = 'maintenance' and auto_transition and status = $1 and `+col+` <= now() returning id::text`, t.from, t.to)
		if err != nil {
			s.log.Error("maintenance transitions", "err", err)
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			s.db.Exec(ctx, `insert into status_incident_updates (incident_id, status, message) values ($1,$2,$3)`, id, t.to, t.message)
			go s.notifySubscribers(context.WithoutCancel(ctx), id)
		}
	}
	if _, err := s.db.Exec(ctx, effectiveStatusSQL, componentStatuses); err != nil {
		s.log.Error("component status", "err", err)
		return
	}
	s.db.Exec(ctx, `update status_component_history h set ended_at = now() from status_components c
		where h.component_id = c.id and h.ended_at is null and h.status <> c.status`)
	s.db.Exec(ctx, `insert into status_component_history (component_id, status, started_at) select id, status, now() from status_components c
		where not exists (select 1 from status_component_history h where h.component_id = c.id and h.ended_at is null)`)
}

// statusPage resolves {id} within the project.
func (s *Server) statusPage(r *http.Request, minRole string) (project, string, error) {
	p, err := s.projectAccess(r, minRole)
	if err != nil {
		return p, "", err
	}
	var id string
	err = s.db.QueryRow(r.Context(), `select id::text from status_pages where project_id = $1 and id::text = $2`, p.ID, r.PathValue("id")).Scan(&id)
	if err == pgx.ErrNoRows {
		return p, "", errStatus(404, "status page not found")
	}
	return p, id, err
}

func (s *Server) statusPageURL(slug string, domain *string) string {
	if domain != nil && *domain != "" {
		return "https://" + *domain
	}
	return s.cfg.AppURL + "/status/" + slug
}

func (s *Server) listStatusPages(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select sp.id, sp.name, sp.slug, sp.domain, sp.public,
		coalesce(sp.domain, '') <> '' as custom_domain, $2 || '/status/' || sp.slug as url,
		(select count(*) from status_components c where c.page_id = sp.id) components,
		(select coalesce(max(array_position($3::text[], c.status)), 1) from status_components c where c.page_id = sp.id) worst,
		(select count(*) from status_incidents i where i.page_id = sp.id and i.kind = 'incident' and i.status <> 'resolved') open_incidents,
		(select count(*) from status_subscribers x where x.page_id = sp.id and x.confirmed_at is not null) subscribers
		from status_pages sp where sp.project_id = $1 order by sp.name`, p.ID, s.cfg.AppURL, componentStatuses)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createStatusPage(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" || len(in.Name) > 80 {
		return errStatus(400, "name is required")
	}
	if !slugRe.MatchString(in.Slug) {
		return errStatus(400, "address must be 2–48 lowercase letters, numbers or dashes")
	}
	out, err := s.one(r.Context(), `insert into status_pages (project_id, name, slug) values ($1,$2,$3) on conflict do nothing returning id`, p.ID, in.Name, in.Slug)
	if err == pgx.ErrNoRows {
		return errStatus(409, "that address is taken")
	} else if err != nil {
		return err
	}
	s.auditProject(r, p, "status_page.create", in.Name, nil)
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) getStatusPage(w http.ResponseWriter, r *http.Request) error {
	_, id, err := s.statusPage(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select sp.id, sp.name, sp.slug, sp.domain, sp.public, sp.description, sp.support_url, sp.brand_color,
		sp.logo is not null as has_logo, $2 || '/status/' || sp.slug as url,
		(select coalesce(json_agg(c order by c.sort, c.group_name, c.position, c.name), '[]') from (select id, name, description, group_name, position,
		   source_type, source_id, manual_status, status, -- the public page's order: a group sits where its first component is
		   case when group_name = '' then position else min(position) over (partition by group_name) end sort
		   from status_components where page_id = sp.id) c) components,
		(select coalesce(json_agg(i order by i.created_at desc), '[]') from (
		   select i.id, i.kind, i.name, i.impact, i.status, i.scheduled_for, i.scheduled_until, i.auto_transition, i.created_at, i.resolved_at,
		     (select coalesce(json_object_agg(component_id, status), '{}') from status_incident_components where incident_id = i.id) components,
		     (select coalesce(json_agg(u order by u.created_at desc), '[]') from (select x.id, x.status, x.message, x.created_at, us.email author
		        from status_incident_updates x left join users us on us.id = x.created_by where x.incident_id = i.id) u) updates
		   from status_incidents i where i.page_id = sp.id order by i.created_at desc limit 100) i) incidents,
		(select count(*) from status_subscribers x where x.page_id = sp.id and x.confirmed_at is not null) subscribers
		from status_pages sp where sp.id::text = $1`, id, s.cfg.AppURL)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) updateStatusPage(w http.ResponseWriter, r *http.Request) error {
	p, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Domain      *string `json:"domain"`
		Public      *bool   `json:"public"`
		Description *string `json:"description"`
		SupportURL  *string `json:"support_url"`
		BrandColor  *string `json:"brand_color"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	set := map[string]any{}
	if in.Name != nil {
		if *in.Name = strings.TrimSpace(*in.Name); *in.Name == "" || len(*in.Name) > 80 {
			return errStatus(400, "name is required")
		}
		set["name"] = *in.Name
	}
	if in.Slug != nil {
		if !slugRe.MatchString(*in.Slug) {
			return errStatus(400, "address must be 2–48 lowercase letters, numbers or dashes")
		}
		set["slug"] = *in.Slug
	}
	if in.Domain != nil {
		d := strings.ToLower(strings.TrimSpace(*in.Domain))
		if u, _ := url.Parse(s.cfg.AppURL); d != "" && (!domainRe.MatchString(d) || (u != nil && d == u.Hostname())) {
			return errStatus(400, "enter a domain such as status.example.com, different from Orbit's own")
		}
		set["domain"] = nilIfEmpty(d)
	}
	if in.Public != nil {
		set["public"] = *in.Public
	}
	if in.Description != nil {
		set["description"] = trunc(strings.TrimSpace(*in.Description), 500)
	}
	if in.SupportURL != nil {
		if u, err := url.Parse(*in.SupportURL); *in.SupportURL != "" && (err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto")) {
			return errStatus(400, "support link must be an http(s) or mailto: address")
		}
		set["support_url"] = *in.SupportURL
	}
	if in.BrandColor != nil {
		if !colorRe.MatchString(*in.BrandColor) {
			return errStatus(400, "brand color must look like #1a2b3c")
		}
		set["brand_color"] = *in.BrandColor
	}
	for col, v := range set { // column names come from the fixed keys above
		if _, err := s.db.Exec(r.Context(), `update status_pages set `+col+` = $2 where id::text = $1`, id, v); err != nil {
			if strings.Contains(err.Error(), "unique") {
				return errStatus(409, "that %s is already used by another status page", map[string]string{"slug": "address", "domain": "domain"}[col])
			}
			return err
		}
	}
	s.auditProject(r, p, "status_page.update", id, map[string]any{"fields": len(set)})
	w.WriteHeader(204)
	return nil
}

func (s *Server) deleteStatusPage(w http.ResponseWriter, r *http.Request) error {
	p, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	var name string
	s.db.QueryRow(r.Context(), `delete from status_pages where id::text = $1 returning name`, id).Scan(&name)
	s.auditProject(r, p, "status_page.delete", name, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) putStatusLogo(w http.ResponseWriter, r *http.Request) error {
	_, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	if r.Method == "DELETE" {
		s.db.Exec(r.Context(), `update status_pages set logo = null, logo_type = null where id::text = $1`, id)
		w.WriteHeader(204)
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 512<<10+1))
	if err != nil {
		return err
	}
	if len(b) > 512<<10 {
		return errStatus(413, "logo must be 512 KB or smaller")
	}
	// SVG is refused: served from Orbit's own origin it could run script.
	typ := http.DetectContentType(b)
	if typ != "image/png" && typ != "image/jpeg" && typ != "image/webp" && typ != "image/gif" {
		return errStatus(415, "logo must be a PNG, JPEG, WebP or GIF image")
	}
	_, err = s.db.Exec(r.Context(), `update status_pages set logo = $2, logo_type = $3 where id::text = $1`, id, b, typ)
	if err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

type componentInput struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	GroupName    *string `json:"group_name"`
	Position     *int    `json:"position"`
	SourceType   *string `json:"source_type"`
	SourceID     *string `json:"source_id"`
	ManualStatus *string `json:"manual_status"`
}

func (s *Server) saveComponent(w http.ResponseWriter, r *http.Request) error {
	p, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	var in componentInput
	if err := decode(r, &in); err != nil {
		return err
	}
	cid := r.PathValue("cid")
	if cid == "" {
		var newID string
		if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
			return errStatus(400, "name is required")
		}
		if err := s.db.QueryRow(r.Context(), `insert into status_components (page_id, name, position)
			values ($1::uuid, $2, (select coalesce(max(position), 0) + 1 from status_components where page_id = $1::uuid)) returning id::text`,
			id, strings.TrimSpace(*in.Name)).Scan(&newID); err != nil {
			return err
		}
		cid = newID
	}
	if in.SourceType != nil {
		src, sid := *in.SourceType, ""
		if in.SourceID != nil {
			sid = *in.SourceID
		}
		table := map[string]string{"monitor": "monitors", "server": "servers", "app": "applications"}[src]
		if src != "manual" {
			var ok bool
			if table != "" {
				s.db.QueryRow(r.Context(), `select exists(select 1 from `+table+` where project_id = $1 and id::text = $2)`, p.ID, sid).Scan(&ok)
			}
			if !ok {
				return errStatus(400, "choose a monitor, server or service from this project")
			}
		}
		s.db.Exec(r.Context(), `update status_components set source_type = $3, source_id = $4 where page_id::text = $1 and id::text = $2`,
			id, cid, src, nilIfEmpty(sid))
	}
	if in.ManualStatus != nil && !oneOf(*in.ManualStatus, componentStatuses) {
		return errStatus(400, "invalid status")
	}
	str := func(p *string, n int) any {
		if p == nil {
			return nil
		}
		return trunc(strings.TrimSpace(*p), n)
	}
	tag, err := s.db.Exec(r.Context(), `update status_components set name = coalesce(nullif($3, ''), name), description = coalesce($4, description),
		group_name = coalesce($5, group_name), position = coalesce($6, position), manual_status = coalesce($7, manual_status)
		where page_id::text = $1 and id::text = $2`, id, cid, str(in.Name, 80), str(in.Description, 300), str(in.GroupName, 80), in.Position, in.ManualStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errStatus(404, "component not found")
	}
	s.updateStatusPages(r.Context())
	writeJSON(w, 200, map[string]string{"id": cid})
	return nil
}

func (s *Server) deleteComponent(w http.ResponseWriter, r *http.Request) error {
	_, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	s.db.Exec(r.Context(), `delete from status_components where page_id::text = $1 and id::text = $2`, id, r.PathValue("cid"))
	w.WriteHeader(204)
	return nil
}

type incidentInput struct {
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Impact         string            `json:"impact"`
	Status         string            `json:"status"`
	Message        string            `json:"message"`
	Components     map[string]string `json:"components"` // component id → status while open
	ScheduledFor   *time.Time        `json:"scheduled_for"`
	ScheduledUntil *time.Time        `json:"scheduled_until"`
	AutoTransition *bool             `json:"auto_transition"`
	Notify         bool              `json:"notify"`
}

// setIncidentComponents replaces an incident's affected components, all of which must be on its page.
func setIncidentComponents(ctx context.Context, tx pgx.Tx, pageID, incidentID string, comps map[string]string) error {
	if _, err := tx.Exec(ctx, `delete from status_incident_components where incident_id = $1`, incidentID); err != nil {
		return err
	}
	for cid, st := range comps {
		if !oneOf(st, componentStatuses) {
			return errStatus(400, "invalid component status %q", st)
		}
		tag, err := tx.Exec(ctx, `insert into status_incident_components select $1, id, $3 from status_components where page_id::text = $2 and id::text = $4`,
			incidentID, pageID, st, cid)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errStatus(400, "component not found on this page")
		}
	}
	return nil
}

func (s *Server) createIncident(w http.ResponseWriter, r *http.Request) error {
	p, pageID, err := s.statusPage(r, "member")
	if err != nil {
		return err
	}
	var in incidentInput
	if err := decode(r, &in); err != nil {
		return err
	}
	statuses, ok := incidentStatuses[in.Kind]
	if !ok {
		return errStatus(400, "kind must be incident or maintenance")
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" || len(in.Name) > 200 {
		return errStatus(400, "name is required")
	}
	if in.Message = strings.TrimSpace(in.Message); in.Message == "" {
		return errStatus(400, "write a message for the first update")
	}
	if in.Kind == "maintenance" {
		in.Impact = "maintenance"
		if in.Status == "" {
			in.Status = "scheduled"
		}
		if in.ScheduledFor == nil || in.ScheduledUntil == nil || !in.ScheduledUntil.After(*in.ScheduledFor) {
			return errStatus(400, "maintenance needs a start and an end after it")
		}
	} else if !oneOf(in.Impact, []string{"none", "minor", "major", "critical"}) {
		return errStatus(400, "impact must be none, minor, major or critical")
	}
	if !oneOf(in.Status, statuses) {
		return errStatus(400, "status must be one of %s", strings.Join(statuses, ", "))
	}
	auto := in.AutoTransition == nil || *in.AutoTransition
	var iid string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `insert into status_incidents (page_id, kind, name, impact, status, scheduled_for, scheduled_until, auto_transition, resolved_at)
			values ($1,$2,$3,$4,$5,$6,$7,$8, case when $5 in ('resolved','completed') then now() end) returning id::text`,
			pageID, in.Kind, in.Name, in.Impact, in.Status, in.ScheduledFor, in.ScheduledUntil, auto).Scan(&iid); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `insert into status_incident_updates (incident_id, status, message, created_by) values ($1,$2,$3,$4)`,
			iid, in.Status, in.Message, currentUser(r).ID); err != nil {
			return err
		}
		return setIncidentComponents(r.Context(), tx, pageID, iid, in.Components)
	})
	if err != nil {
		return err
	}
	s.auditProject(r, p, "status_incident.create", in.Name, map[string]any{"kind": in.Kind, "impact": in.Impact})
	s.updateStatusPages(r.Context())
	if in.Notify {
		go s.notifySubscribers(context.WithoutCancel(r.Context()), iid)
	}
	writeJSON(w, 201, map[string]string{"id": iid})
	return nil
}

// addIncidentUpdate posts an update, optionally changing the status and affected components.
func (s *Server) addIncidentUpdate(w http.ResponseWriter, r *http.Request) error {
	p, pageID, err := s.statusPage(r, "member")
	if err != nil {
		return err
	}
	var in struct {
		Status     string            `json:"status"`
		Message    string            `json:"message"`
		Components map[string]string `json:"components"`
		Notify     bool              `json:"notify"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var kind, name string
	iid := r.PathValue("iid")
	if err := s.db.QueryRow(r.Context(), `select kind, name from status_incidents where page_id::text = $1 and id::text = $2`, pageID, iid).Scan(&kind, &name); err != nil {
		return err
	}
	if !oneOf(in.Status, incidentStatuses[kind]) {
		return errStatus(400, "status must be one of %s", strings.Join(incidentStatuses[kind], ", "))
	}
	if in.Message = strings.TrimSpace(in.Message); in.Message == "" {
		return errStatus(400, "write a message")
	}
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update status_incidents set status = $2,
			resolved_at = case when $2 in ('resolved','completed') then coalesce(resolved_at, now()) end where id::text = $1`, iid, in.Status); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `insert into status_incident_updates (incident_id, status, message, created_by) values ($1,$2,$3,$4)`,
			iid, in.Status, in.Message, currentUser(r).ID); err != nil {
			return err
		}
		if in.Components != nil {
			return setIncidentComponents(r.Context(), tx, pageID, iid, in.Components)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.auditProject(r, p, "status_incident.update", name, map[string]any{"status": in.Status})
	s.updateStatusPages(r.Context())
	if in.Notify {
		go s.notifySubscribers(context.WithoutCancel(r.Context()), iid)
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) deleteIncident(w http.ResponseWriter, r *http.Request) error {
	p, pageID, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from status_incidents where page_id::text = $1 and id::text = $2 returning name`,
		pageID, r.PathValue("iid")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "status_incident.delete", name, nil)
	s.updateStatusPages(r.Context())
	w.WriteHeader(204)
	return nil
}

func (s *Server) listSubscribers(w http.ResponseWriter, r *http.Request) error {
	_, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select id, email, webhook_url, confirmed_at, created_at from status_subscribers where page_id::text = $1
		order by created_at desc limit 1000`, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) deleteSubscriber(w http.ResponseWriter, r *http.Request) error {
	_, id, err := s.statusPage(r, "admin")
	if err != nil {
		return err
	}
	s.db.Exec(r.Context(), `delete from status_subscribers where page_id::text = $1 and id::text = $2`, id, r.PathValue("sid"))
	w.WriteHeader(204)
	return nil
}

var statusLabels = map[string]string{
	"investigating": "Investigating", "identified": "Identified", "monitoring": "Monitoring", "resolved": "Resolved",
	"scheduled": "Scheduled", "in_progress": "In progress", "verifying": "Verifying", "completed": "Completed",
	"operational": "Operational", "degraded_performance": "Degraded performance", "partial_outage": "Partial outage",
	"major_outage": "Major outage", "under_maintenance": "Under maintenance",
}

var impactColors = map[string]string{"none": "#1f2937", "minor": "#d97706", "major": "#ea580c", "critical": "#dc2626", "maintenance": "#2563eb"}

// notifySubscribers sends an incident's latest update to every confirmed subscriber of its page.
func (s *Server) notifySubscribers(ctx context.Context, incidentID string) {
	var pageID, slug, pageName, name, kind, impact, status, message string
	var domain *string
	err := s.db.QueryRow(ctx, `select sp.id::text, sp.slug, sp.domain, sp.name, i.name, i.kind, i.impact, u.status, u.message
		from status_incidents i join status_pages sp on sp.id = i.page_id
		join lateral (select status, message from status_incident_updates where incident_id = i.id order by created_at desc limit 1) u on true
		where i.id::text = $1`, incidentID).Scan(&pageID, &slug, &domain, &pageName, &name, &kind, &impact, &status, &message)
	if err != nil {
		return
	}
	var comps []string
	if rows, err := s.db.Query(ctx, `select c.name from status_incident_components ic join status_components c on c.id = ic.component_id
		where ic.incident_id::text = $1 order by c.name`, incidentID); err == nil {
		for rows.Next() {
			var n string
			rows.Scan(&n)
			comps = append(comps, n)
		}
		rows.Close()
	}
	base := s.statusPageURL(slug, domain)
	link := base + "/incidents/" + incidentID
	rows, err := s.db.Query(ctx, `select coalesce(email, ''), coalesce(webhook_url, ''), token from status_subscribers
		where page_id::text = $1 and confirmed_at is not null`, pageID)
	if err != nil {
		return
	}
	type sub struct{ email, hook, token string }
	var subs []sub
	for rows.Next() {
		var x sub
		rows.Scan(&x.email, &x.hook, &x.token)
		subs = append(subs, x)
	}
	rows.Close()
	affected := strings.Join(comps, ", ")
	if affected == "" {
		affected = "—"
	}
	payload := map[string]any{"page": map[string]string{"id": pageID, "name": pageName, "url": base},
		"incident": map[string]any{"id": incidentID, "kind": kind, "name": name, "impact": impact, "status": status, "url": link,
			"components": comps, "update": map[string]any{"status": status, "body": message, "created_at": time.Now().UTC()}}}
	for _, x := range subs {
		if x.hook != "" {
			if err := s.postJSON(ctx, x.hook, payload); err != nil {
				s.log.Warn("status webhook failed", "page", slug, "err", err)
			}
			continue
		}
		err := s.sendTemplate([]string{x.email}, "["+pageName+"] "+name, "status-update", map[string]string{
			"PageName": pageName, "Title": name, "Status": statusLabels[status], "Color": impactColors[impact], "Message": message,
			"Components": affected, "Link": link, "Unsubscribe": base + "/unsubscribe/" + x.token,
		})
		if err != nil {
			s.log.Warn("status email failed", "page", slug, "err", err)
		}
	}
}
