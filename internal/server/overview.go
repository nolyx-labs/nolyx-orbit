// The project overview dashboard.
package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) overview(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := map[string]json.RawMessage{}
	queries := map[string]string{
		"servers": `select count(*) as total, count(*) filter (where status = 'online') as online,
			coalesce(sum(cpu_count),0) as cpus, coalesce(sum(mem_total),0) as mem_total
			from servers where project_id = $1`,
		"containers": `select count(*) as total, count(*) filter (where c.state = 'running') as running,
			count(*) filter (where c.health = 'unhealthy' or c.state in ('restarting','dead')) as unhealthy
			from containers c join servers s on s.id = c.server_id where s.project_id = $1`,
		"apps": `select count(*) as total,
			count(*) filter (where exists (select 1 from containers c where c.server_id = a.server_id
			  and c.labels->>'orbit.app' = a.name and c.state = 'running' and c.health <> 'unhealthy')
			  or exists (select 1 from nomad_clusters n, jsonb_array_elements(n.allocations) al where n.id = a.nomad_cluster_id
			  and al->>'JobID' = a.name and al->>'ClientStatus' = 'running')) as healthy,
			count(*) filter (where a.proxy) as proxied
			from applications a where a.project_id = $1`,
		"databases": `select count(*) filter (where role <> 'replica') as total, count(*) filter (where role <> 'replica' and status = 'healthy') as healthy,
			count(*) filter (where role = 'replica') as replicas, count(*) filter (where role = 'replica' and status = 'healthy') as replicas_healthy,
			count(*) filter (where status in ('degraded','down','failed')) as unhealthy
			from databases where project_id = $1`,
		"usage": `select avg(m.cpu) as cpu, avg(m.mem_used::float8 / nullif(m.mem_total,0)) * 100 as mem,
			avg(m.disk_used::float8 / nullif(m.disk_total,0)) * 100 as disk, sum(m.net_rx + m.net_tx) as net
			from servers s cross join lateral (select * from server_metrics where server_id = s.id order by ts desc limit 1) m
			where s.project_id = $1 and s.status = 'online'`,
	}
	for k, q := range queries {
		if out[k], err = s.one(ctx, q, p.ID); err != nil {
			return err
		}
	}
	if out["deployments"], err = s.list(ctx, deploymentsSQL+` where a.project_id = $1 order by d.created_at desc limit 6`, p.ID); err != nil {
		return err
	}
	if out["events"], err = s.list(ctx, `select e.id, e.severity, e.message, e.created_at, e.server_id from events e where project_id = $1 order by id desc limit 12`, p.ID); err != nil {
		return err
	}
	if out["attention"], err = s.list(ctx, attentionSQL, p.ID); err != nil {
		return err
	}
	if out["incidents"], err = s.list(ctx, incidentsSQL+` and i.resolved_at is null order by i.opened_at desc`, p.ID); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// attentionSQL lists what needs action beyond alert incidents: one row per server or monitor, n > 0.
const attentionSQL = `select * from (
	select 'security_updates' as kind, s.id, s.name,
	  (select count(*) from jsonb_array_elements(c.packages) p where (p->>'security')::boolean) as n
	  from servers s join os_update_checks c on c.server_id = s.id where s.project_id = $1
	union all select 'reboot', s.id, s.name, 1
	  from servers s join os_update_checks c on c.server_id = s.id where s.project_id = $1 and c.reboot_required
	union all select 'backup_failed', s.id, s.name, 1
	  from servers s where s.project_id = $1
	  and (select b.status from server_backups b where b.server_id = s.id order by b.created_at desc limit 1) = 'failed'
	union all select 'findings', s.id, s.name,
	  (select count(*) from jsonb_array_elements(case jsonb_typeof(i.scan->'findings') when 'array' then i.scan->'findings' else '[]' end) f
	   where f->>'severity' = 'high')
	  from servers s join server_insight i on i.server_id = s.id where s.project_id = $1
	union all select 'monitor_down', m.id, m.name, 1
	  from monitors m where m.project_id = $1 and not m.paused and m.status = 'down'
) a where n > 0 order by kind, name`

// fleet summarizes every project the user can see, across organizations (for agencies with one org per client).
// Organizations that block the user over 2FA are left out; API keys only see their own organization or project.
func (s *Server) fleet(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	var keyOrg, keyProject string
	if u.Key != nil {
		keyOrg, keyProject = u.Key.OrgID, u.Key.ProjectID
	}
	out, err := s.list(r.Context(), `select o.id as org_id, o.name as org, p.id, p.name,
		  (select count(*) from servers where project_id = p.id) as servers,
		  (select count(*) from servers where project_id = p.id and status = 'online') as online,
		  (select count(*) from applications where project_id = p.id) as apps,
		  (select count(*) from databases where project_id = p.id and role <> 'replica') as databases,
		  (select count(*) from alert_incidents i join alert_rules ar on ar.id = i.rule_id where ar.project_id = p.id and i.resolved_at is null) as incidents,
		  (select count(*) from (`+strings.ReplaceAll(attentionSQL, "$1", "p.id")+`) a) as attention
		from memberships m join organizations o on o.id = m.org_id join projects p on p.org_id = o.id
		where m.user_id = $1 and not mfa_blocked(o.id, m.user_id) and ($2 = '' or o.id::text = $2) and ($3 = '' or p.id::text = $3)
		order by o.created_at, p.created_at`, u.ID, keyOrg, keyProject)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
