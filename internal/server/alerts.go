package server

import (
	"context"
	"fmt"
)

// alertMetrics: what each rule measures, its target and its unit. Threshold-less metrics use duration only.
var alertMetrics = map[string]struct{ target, unit string }{
	"cpu": {"server", "%"}, "memory": {"server", "%"}, "disk": {"server", "%"}, "io": {"server", "%"}, "offline": {"server", ""},
	"replication_lag": {"database", "s"}, "db_connections": {"database", "%"}, "db_down": {"database", ""},
	"error_rate": {"app", "%"}, "container_restarts": {"container", ""}, "deployment_failed": {"app", ""},
}

// alertSQL yields one row per (rule, target): rule fields, target, current value and whether it breaches.
const alertSQL = `
with rules as (select * from alert_rules where metric <> 'deployment_failed')
select r.id::text, r.name, r.metric, coalesce(r.threshold, 0), r.webhook_url, r.channel_ids::text[], r.project_id::text,
  'server', s.id::text, s.name, s.id::text,
  coalesce(case r.metric
    when 'cpu' then avg(m.cpu) when 'io' then avg(m.io_util)
    when 'memory' then avg(m.mem_used::float8 / nullif(m.mem_total,0)) * 100
    when 'disk' then avg(m.disk_used::float8 / nullif(m.disk_total,0)) * 100 end, 0),
  count(m.ts) > 0 and case r.metric
    when 'cpu' then avg(m.cpu) when 'io' then avg(m.io_util)
    when 'memory' then avg(m.mem_used::float8 / nullif(m.mem_total,0)) * 100
    else avg(m.disk_used::float8 / nullif(m.disk_total,0)) * 100 end > r.threshold
from rules r join servers s on s.project_id = r.project_id
left join server_metrics m on m.server_id = s.id and m.ts > now() - make_interval(mins => r.duration_minutes)
where r.metric in ('cpu','memory','disk','io') group by r.id, r.name, r.metric, r.threshold, r.webhook_url, r.channel_ids, r.project_id, s.id
union all
select r.id::text, r.name, r.metric, 0, r.webhook_url, r.channel_ids::text[], r.project_id::text, 'server', s.id::text, s.name, s.id::text,
  coalesce(extract(epoch from now() - s.last_seen_at) / 60, 0),
  s.status = 'offline' and coalesce(s.last_seen_at, s.created_at) < now() - make_interval(mins => r.duration_minutes)
from rules r join servers s on s.project_id = r.project_id where r.metric = 'offline'
union all
select r.id::text, r.name, r.metric, coalesce(r.threshold, 0), r.webhook_url, r.channel_ids::text[], r.project_id::text,
  'database', d.id::text, d.name, d.server_id::text,
  coalesce(case r.metric when 'replication_lag' then avg(m.lag_ms) / 1000
    else avg(m.connections::float8 / nullif(m.max_connections,0)) * 100 end, 0),
  count(m.ts) > 0 and case r.metric when 'replication_lag' then avg(m.lag_ms) / 1000
    else avg(m.connections::float8 / nullif(m.max_connections,0)) * 100 end > r.threshold
from rules r join databases d on d.project_id = r.project_id
left join database_metrics m on m.database_id = d.id and m.ts > now() - make_interval(mins => r.duration_minutes)
where r.metric in ('replication_lag','db_connections') and (r.metric <> 'replication_lag' or d.role = 'replica')
group by r.id, r.name, r.metric, r.threshold, r.webhook_url, r.channel_ids, r.project_id, d.id
union all
select r.id::text, r.name, r.metric, 0, r.webhook_url, r.channel_ids::text[], r.project_id::text, 'database', d.id::text, d.name, d.server_id::text,
  0, d.status = 'down' and not exists (select 1 from database_metrics m where m.database_id = d.id and m.ts > now() - make_interval(mins => r.duration_minutes))
from rules r join databases d on d.project_id = r.project_id where r.metric = 'db_down'
union all
select r.id::text, r.name, r.metric, coalesce(r.threshold, 0), r.webhook_url, r.channel_ids::text[], r.project_id::text,
  'app', a.id::text, a.name, a.server_id::text,
  coalesce(sum(m.errors) * 100.0 / nullif(sum(m.requests), 0), 0),
  coalesce(sum(m.requests), 0) >= 20 and coalesce(sum(m.errors) * 100.0 / nullif(sum(m.requests), 0), 0) > r.threshold
from rules r join applications a on a.project_id = r.project_id
left join app_metrics m on m.application_id = a.id and m.ts > now() - make_interval(mins => r.duration_minutes)
where r.metric = 'error_rate' group by r.id, r.name, r.metric, r.threshold, r.webhook_url, r.channel_ids, r.project_id, a.id
union all
select r.id::text, r.name, r.metric, 0, r.webhook_url, r.channel_ids::text[], r.project_id::text,
  'container', c.server_id::text || '/' || c.docker_id, c.name, c.server_id::text, c.restart_count,
  c.state in ('restarting','dead') or c.health = 'unhealthy'
from rules r join servers s on s.project_id = r.project_id join containers c on c.server_id = s.id
where r.metric = 'container_restarts'`

type alertState struct {
	RuleID, Rule, Metric, Webhook, ProjectID   string
	Channels                                   []string
	TargetType, TargetID, TargetName, ServerID string
	Threshold, Value                           float64
	Breach                                     bool
}

func (a alertState) describe(status string) string {
	unit := alertMetrics[a.Metric].unit
	v := ""
	if unit != "" {
		v = fmt.Sprintf(" (%.1f%s, threshold %.1f%s)", a.Value, unit, a.Threshold, unit)
	}
	return fmt.Sprintf("Alert %s %s on %s %s%s", a.Rule, status, a.TargetType, a.TargetName, v)
}

func (s *Server) evaluateAlerts(ctx context.Context) {
	rows, err := s.db.Query(ctx, alertSQL)
	if err != nil {
		s.log.Error("evaluate alerts", "err", err)
		return
	}
	var states []alertState
	for rows.Next() {
		var a alertState
		if err := rows.Scan(&a.RuleID, &a.Rule, &a.Metric, &a.Threshold, &a.Webhook, &a.Channels, &a.ProjectID,
			&a.TargetType, &a.TargetID, &a.TargetName, &a.ServerID, &a.Value, &a.Breach); err != nil {
			s.log.Error("scan alert", "err", err)
			continue
		}
		states = append(states, a)
	}
	rows.Close()
	for _, a := range states {
		s.transition(ctx, a)
	}
}

// transition opens or resolves an incident when a target's breach state changes, then notifies.
func (s *Server) transition(ctx context.Context, a alertState) {
	var id int64
	var err error
	if a.Breach {
		err = s.db.QueryRow(ctx, `insert into alert_incidents (rule_id, server_id, value, target_type, target_id, target_name)
			values ($1, nullif($2,'')::uuid, $3, $4, $5, $6) on conflict (rule_id, target_id) where resolved_at is null do nothing returning id`,
			a.RuleID, a.ServerID, a.Value, a.TargetType, a.TargetID, a.TargetName).Scan(&id)
	} else {
		err = s.db.QueryRow(ctx, `update alert_incidents set resolved_at = now()
			where rule_id = $1 and target_id = $2 and resolved_at is null returning id`, a.RuleID, a.TargetID).Scan(&id)
	}
	if err != nil {
		return // no state change
	}
	status, sev := "resolved", "info"
	if a.Breach {
		status, sev = "firing", "error"
	}
	s.event(ctx, a.ProjectID, a.ServerID, sev, a.describe(status))
	s.hub.publish(a.ProjectID, "alerts", "")
	go s.notify(context.WithoutCancel(ctx), a, status)
}

// deploymentAlert fires deployment_failed rules on failure and resolves them on the next success.
func (s *Server) deploymentAlert(ctx context.Context, projectID, appID, app string, number int, ok bool) {
	rows, err := s.db.Query(ctx, `select id::text, name, webhook_url, channel_ids::text[] from alert_rules where project_id::text = $1 and metric = 'deployment_failed'`, projectID)
	if err != nil {
		return
	}
	var states []alertState
	for rows.Next() {
		a := alertState{Metric: "deployment_failed", ProjectID: projectID, TargetType: "app", TargetID: appID,
			TargetName: fmt.Sprintf("%s (deployment #%d)", app, number), Breach: !ok}
		rows.Scan(&a.RuleID, &a.Rule, &a.Webhook, &a.Channels)
		states = append(states, a)
	}
	rows.Close()
	for _, a := range states {
		s.transition(ctx, a)
	}
}
