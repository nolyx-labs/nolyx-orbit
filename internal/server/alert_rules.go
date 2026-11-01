// Alert rule and incident API.
package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const incidentsSQL = `select i.id, i.value, i.opened_at, i.resolved_at, i.target_type, i.target_id, i.target_name, i.acknowledged_at,
	ack.email as acknowledged_by, r.name as rule, r.metric, r.threshold, i.server_id
	from alert_incidents i join alert_rules r on r.id = i.rule_id left join users ack on ack.id = i.acknowledged_by where r.project_id = $1`

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rules, err := s.list(r.Context(), `select id, name, metric, threshold, duration_minutes, webhook_url <> '' as has_webhook, channel_ids, created_at,
		(select count(*) from alert_incidents i where i.rule_id = r.id and i.resolved_at is null) as firing
		from alert_rules r where project_id = $1 order by created_at`, p.ID)
	if err != nil {
		return err
	}
	incidents, err := s.list(r.Context(), incidentsSQL+` order by i.resolved_at is null desc, i.opened_at desc limit 100`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]json.RawMessage{"rules": rules, "incidents": incidents})
	return nil
}

func (s *Server) createAlert(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in struct {
		Name            string   `json:"name"`
		Metric          string   `json:"metric"`
		Threshold       float64  `json:"threshold"`
		DurationMinutes int      `json:"duration_minutes"`
		WebhookURL      string   `json:"webhook_url"`
		ChannelIDs      []string `json:"channel_ids"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	m, ok := alertMetrics[in.Metric]
	if !ok {
		return errStatus(400, "unknown metric")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errStatus(400, "name is required")
	}
	if m.unit == "%" && (in.Threshold <= 0 || in.Threshold > 100) {
		return errStatus(400, "threshold must be between 0 and 100 percent")
	}
	if m.unit == "s" && in.Threshold <= 0 {
		return errStatus(400, "threshold must be positive")
	}
	if in.DurationMinutes < 1 || in.DurationMinutes > 1440 {
		return errStatus(400, "duration must be 1–1440 minutes")
	}
	if in.WebhookURL != "" {
		if u, err := url.Parse(in.WebhookURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errStatus(400, "webhook URL must be http(s)")
		}
	}
	if in.ChannelIDs == nil {
		in.ChannelIDs = []string{}
	}
	var n int
	s.db.QueryRow(r.Context(), `select count(*) from notification_channels where project_id = $1 and id::text = any($2)`, p.ID, in.ChannelIDs).Scan(&n)
	if n != len(in.ChannelIDs) {
		return errStatus(400, "unknown notification channel")
	}
	out, err := s.one(r.Context(), `insert into alert_rules (project_id, name, metric, threshold, duration_minutes, webhook_url, channel_ids)
		values ($1,$2,$3,$4,$5,$6,$7::uuid[]) returning id`, p.ID, in.Name, in.Metric, in.Threshold, in.DurationMinutes, in.WebhookURL, in.ChannelIDs)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "alert.create", in.Name, map[string]any{"metric": in.Metric, "threshold": in.Threshold})
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) deleteAlert(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from alert_rules where project_id = $1 and id::text = $2 returning name`, p.ID, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "alert.delete", name, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) ackIncident(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `update alert_incidents i set acknowledged_by = $3, acknowledged_at = now()
		from alert_rules r where r.id = i.rule_id and r.project_id = $1 and i.id::text = $2 and i.acknowledged_at is null`, p.ID, r.PathValue("id"), currentUser(r).ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errStatus(404, "incident not found or already acknowledged")
	}
	s.auditProject(r, p, "incident.acknowledge", r.PathValue("id"), nil)
	s.hub.publish(p.ID, "alerts", "")
	w.WriteHeader(204)
	return nil
}
