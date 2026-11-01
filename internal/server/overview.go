// The project overview dashboard.
package server

import (
	"encoding/json"
	"net/http"
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
	if out["incidents"], err = s.list(ctx, incidentsSQL+` and i.resolved_at is null order by i.opened_at desc`, p.ID); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
