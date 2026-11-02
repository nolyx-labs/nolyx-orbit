// Server maintenance windows: alerts are held and status pages show "under maintenance" while one is
// active (servers_on_hold, used by maintenanceSQL and effectiveStatusSQL). Optionally the window starts
// by restarting the server or installing its updates, and then ends once the server is back.
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// runMaintenance starts due windows and ends finished ones.
func (s *Server) runMaintenance(ctx context.Context) {
	rows, err := s.db.Query(ctx, `update server_maintenance m set status = 'active' from servers sv
		where sv.id = m.server_id and m.status = 'planned' and m.starts_at <= now()
		returning m.id::text, m.project_id::text, m.server_id::text, sv.name, m.action, coalesce(m.created_by::text, '')`)
	if err != nil {
		s.log.Error("start maintenance", "err", err)
		return
	}
	type window struct{ id, project, server, name, action, user string }
	var started []window
	for rows.Next() {
		var w window
		rows.Scan(&w.id, &w.project, &w.server, &w.name, &w.action, &w.user)
		started = append(started, w)
	}
	rows.Close()
	for _, w := range started {
		s.event(ctx, w.project, w.server, "info", "Maintenance started on "+w.name)
		switch w.action {
		case "restart":
			go func() {
				ctx := context.WithoutCancel(ctx)
				if err := s.reboot(ctx, w.project, w.server, w.user); err != nil {
					s.db.Exec(ctx, `update server_maintenance set result = $2 where id = $1`, w.id, "Couldn't restart the server: "+err.Error())
				} else {
					s.db.Exec(ctx, `update server_maintenance set action_at = now(), result = 'Restarting the server' where id = $1`, w.id)
				}
				s.hub.publish(w.project, "maintenance", w.server)
			}()
		case "update":
			rid, err := s.startRollout(ctx, w.project, w.user, rolloutInput{Scope: "all", Reboot: "if_required", ServerIDs: []string{w.server}})
			if err != nil {
				s.db.Exec(ctx, `update server_maintenance set result = $2 where id = $1`, w.id, "Couldn't start installing updates: "+err.Error())
			} else {
				s.db.Exec(ctx, `update server_maintenance set action_at = now(), rollout_id = $2, result = 'Installing updates' where id = $1`, w.id, rid)
				s.claimRollouts(ctx)
			}
		}
		s.hub.publish(w.project, "maintenance", w.server)
	}

	// The window ends on time, or early once its restart or update is over and the server is back.
	rows, err = s.db.Query(ctx, `with boot as (select m.id, (select mt.ts - make_interval(secs => mt.uptime) from server_metrics mt
		  where mt.server_id = m.server_id order by mt.ts desc limit 1) booted_at from server_maintenance m where m.status = 'active')
		update server_maintenance m set status = 'done', ends_at = least(m.ends_at, now()),
		  result = case when m.ends_at <= now() or m.action = 'none' then m.result
		    when m.action = 'restart' then 'Restarted; the server is back'
		    else coalesce((select rs.result from os_rollout_servers rs where rs.rollout_id = m.rollout_id and rs.server_id = m.server_id), m.result) end
		from boot b, servers sv where b.id = m.id and sv.id = m.server_id and (m.ends_at <= now() or (m.action_at is not null and sv.status = 'online' and (
		  (m.action = 'restart' and b.booted_at > m.action_at) or
		  (m.action = 'update' and exists (select 1 from os_rollouts o join os_rollout_servers rs on rs.rollout_id = o.id
		    where o.id = m.rollout_id and o.status in ('done','failed','cancelled') and (not rs.rebooting or b.booted_at > m.action_at))))))
		returning m.project_id::text, m.server_id::text, sv.name, m.incident_ids::text[]`)
	if err != nil {
		s.log.Error("end maintenance", "err", err)
		return
	}
	var ended []window
	var incidents []string
	for rows.Next() {
		var w window
		var ids []string
		rows.Scan(&w.project, &w.server, &w.name, &ids)
		ended = append(ended, w)
		incidents = append(incidents, ids...)
	}
	rows.Close()
	s.endIncidents(ctx, incidents)
	for _, w := range ended {
		s.event(ctx, w.project, w.server, "info", "Maintenance ended on "+w.name)
		s.hub.publish(w.project, "maintenance", w.server)
	}
}

// endIncidents brings the status page entries' end forward; updateStatusPages then completes them.
func (s *Server) endIncidents(ctx context.Context, ids []string) {
	if len(ids) > 0 {
		s.db.Exec(ctx, `update status_incidents set scheduled_until = greatest(scheduled_for + interval '1 second', now())
			where id::text = any($1) and status in ('scheduled','in_progress')`, ids)
	}
}

func (s *Server) listMaintenance(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	// The quiet hour is the UTC hour of day with the lowest average CPU over the last two weeks.
	out, err := s.one(r.Context(), `select
		(select coalesce(json_agg(x order by x.starts_at desc), '[]') from (select m.id, m.starts_at, m.ends_at, m.reason, m.action, m.status,
		   m.result, m.rollout_id, cardinality(m.incident_ids) status_pages, u.email created_by
		   from server_maintenance m left join users u on u.id = m.created_by where m.server_id = sv.id order by m.starts_at desc limit 20) x) windows,
		(select json_build_object('hour', h, 'cpu', round(cpu::numeric, 1)) from (select extract(hour from ts at time zone 'UTC')::int h, avg(cpu) cpu, count(*) n
		   from server_metrics where server_id = sv.id and ts > now() - interval '14 days' group by 1) x where n >= 30 order by cpu limit 1) quiet_hour
		from servers sv where sv.project_id = $1 and sv.id::text = $2`, p.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createMaintenance(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	sid, name, err := s.serverInProject(r, p)
	if err != nil {
		return err
	}
	var in struct {
		StartsAt    *time.Time `json:"starts_at"` // empty: now
		Minutes     int        `json:"minutes"`
		Reason      string     `json:"reason"`
		Action      string     `json:"action"` // none | restart | update
		StatusPages bool       `json:"status_pages"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	start := time.Now()
	if in.StartsAt != nil {
		start = *in.StartsAt
	}
	if start.Before(time.Now().Add(-time.Minute)) || start.After(time.Now().AddDate(0, 0, 60)) {
		return errStatus(400, "start time must be between now and 60 days from now")
	}
	if in.Minutes < 30 || in.Minutes > 24*60 {
		return errStatus(400, "duration must be 30 minutes to 24 hours")
	}
	if in.Reason = strings.TrimSpace(in.Reason); len(in.Reason) > 200 {
		return errStatus(400, "keep the reason under 200 characters")
	}
	if in.Action == "" {
		in.Action = "none"
	}
	if !oneOf(in.Action, []string{"none", "restart", "update"}) {
		return errStatus(400, "action must be none, restart or update")
	}
	end := start.Add(time.Duration(in.Minutes) * time.Minute)
	var id string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `insert into server_maintenance (project_id, server_id, starts_at, ends_at, reason, action, created_by)
			values ($1,$2,$3,$4,$5,$6,$7) on conflict do nothing returning id::text`, p.ID, sid, start, end, in.Reason, in.Action, currentUser(r).ID).Scan(&id)
		if err == pgx.ErrNoRows {
			return errStatus(409, "this overlaps another maintenance window on this server")
		}
		if err != nil || !in.StatusPages {
			return err
		}
		message := in.Reason
		if message == "" {
			message = "We're carrying out planned maintenance."
		}
		// One entry per status page with a component for this server or a monitor on it.
		_, err = tx.Exec(r.Context(), `with comps as (select c.id, c.page_id from status_components c join status_pages sp on sp.id = c.page_id
			  where sp.project_id = $1 and ((c.source_type = 'server' and c.source_id = $2)
			    or (c.source_type = 'monitor' and c.source_id in (select id from monitors where server_id = $2)))),
			inc as (insert into status_incidents (page_id, kind, name, impact, status, scheduled_for, scheduled_until)
			  select distinct page_id, 'maintenance', 'Scheduled maintenance', 'maintenance', 'scheduled', $3::timestamptz, $4::timestamptz from comps returning id, page_id),
			ic as (insert into status_incident_components select inc.id, comps.id, 'under_maintenance' from inc join comps on comps.page_id = inc.page_id),
			upd as (insert into status_incident_updates (incident_id, status, message, created_by) select id, 'scheduled', $5, $6 from inc)
			update server_maintenance set incident_ids = (select coalesce(array_agg(id), '{}') from inc) where id = $7`,
			p.ID, sid, start, end, message, currentUser(r).ID, id)
		return err
	})
	if err != nil {
		return err
	}
	s.auditProject(r, p, "server.maintenance.create", name, map[string]any{"id": id, "starts_at": start, "minutes": in.Minutes,
		"action": in.Action, "reason": in.Reason, "status_pages": in.StatusPages})
	s.runMaintenance(r.Context())
	s.updateStatusPages(r.Context())
	s.hub.publish(p.ID, "maintenance", sid)
	writeJSON(w, 201, map[string]string{"id": id})
	return nil
}

// endMaintenance cancels a planned window or ends an active one early.
func (s *Server) endMaintenance(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	sid, name, err := s.serverInProject(r, p)
	if err != nil {
		return err
	}
	var status string
	var incidents []string
	err = s.db.QueryRow(r.Context(), `update server_maintenance set
		status = case when starts_at > now() then 'cancelled' else 'done' end,
		ends_at = case when starts_at > now() then ends_at else greatest(now(), starts_at + interval '1 second') end
		where server_id = $1 and id::text = $2 and status in ('planned','active') returning status, incident_ids::text[]`, sid, r.PathValue("id")).Scan(&status, &incidents)
	if err == pgx.ErrNoRows {
		return errStatus(409, "this maintenance window has already ended")
	} else if err != nil {
		return err
	}
	if status == "cancelled" {
		s.db.Exec(r.Context(), `delete from status_incidents where id::text = any($1)`, incidents)
	} else {
		s.endIncidents(r.Context(), incidents)
	}
	s.auditProject(r, p, "server.maintenance."+map[string]string{"cancelled": "cancel", "done": "end"}[status], name, map[string]any{"id": r.PathValue("id")})
	s.event(r.Context(), p.ID, sid, "info", map[string]string{"cancelled": "Planned maintenance cancelled on ", "done": "Maintenance ended early on "}[status]+name)
	s.updateStatusPages(r.Context())
	s.hub.publish(p.ID, "maintenance", sid)
	w.WriteHeader(204)
	return nil
}
