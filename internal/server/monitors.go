// Uptime monitors: HTTP and TCP checks run from the control plane, and optionally from servers as extra locations,
// with outages and uptime history.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"orbit/internal/protocol"
	"orbit/internal/uptime"
)

type monitor struct {
	ID, ProjectID, Name, Type, Target, ExpectedStatus, Keyword string
	TimeoutMS                                                  int
}

// runMonitors claims due monitors every few seconds and checks them concurrently.
func (s *Server) runMonitors(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	sem := make(chan struct{}, 32)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := s.db.Query(ctx, `update monitors set next_check_at = now() + make_interval(secs => interval_seconds)
			where id in (select id from monitors where not paused and next_check_at <= now() for update skip locked limit 200)
			returning id::text, project_id::text, name, type, target, expected_status, keyword, timeout_ms`)
		if err != nil {
			s.log.Error("claim monitors", "err", err)
			continue
		}
		var due []monitor
		for rows.Next() {
			var m monitor
			rows.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Type, &m.Target, &m.ExpectedStatus, &m.Keyword, &m.TimeoutMS)
			due = append(due, m)
		}
		rows.Close()
		for _, m := range due {
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				s.recordCheck(ctx, m, s.check(ctx, m))
			}()
		}
	}
}

func (s *Server) check(ctx context.Context, m monitor) protocol.MonitorResult {
	return uptime.Check(ctx, protocol.MonitorConfig{ID: m.ID, Type: m.Type, Target: m.Target, ExpectedStatus: m.ExpectedStatus,
		Keyword: m.Keyword, TimeoutMS: m.TimeoutMS}, s.safeDialer())
}

// recordCheck stores a control-plane result and opens or closes an outage. Two failures in a row mark a monitor
// down, so one dropped packet isn't an outage. Alert rules on monitor_down pick the change up (alertSQL).
func (s *Server) recordCheck(ctx context.Context, m monitor, r protocol.MonitorResult) {
	if _, err := s.db.Exec(ctx, `insert into monitor_checks (monitor_id, ok, latency_ms, status_code, error) values ($1,$2,$3,nullif($4,0),$5)`,
		m.ID, r.OK, r.LatencyMS, r.StatusCode, r.Error); err != nil {
		s.log.Error("record check", "monitor", m.Name, "err", err)
		return
	}
	r = s.vote(ctx, m.ID, r)
	var before, after string
	err := s.db.QueryRow(ctx, `with prev as (select status from monitors where id = $1)
		update monitors set
		  fail_streak = case when $2 then 0 else fail_streak + 1 end,
		  status = case when $2 then 'up' when fail_streak + 1 >= 2 then 'down' else status end,
		  last_checked_at = now(), last_latency_ms = $3, last_error = $4, cert_expires_at = coalesce($5, cert_expires_at)
		where id = $1 returning (select status from prev), status`, m.ID, r.OK, r.LatencyMS, r.Error, r.CertExpiry).Scan(&before, &after)
	if err != nil {
		return // deleted mid-check
	}
	switch {
	case before != "down" && after == "down":
		s.db.Exec(ctx, `insert into monitor_outages (monitor_id, started_at, error)
			select $1, coalesce((select min(ts) from monitor_checks where monitor_id = $1 and location is null and ts > coalesce(
			  (select max(ts) from monitor_checks where monitor_id = $1 and location is null and ok), '-infinity')), now()), $2
			on conflict do nothing`, m.ID, r.Error)
		s.event(ctx, m.ProjectID, "", "error", "Monitor "+m.Name+" is down: "+r.Error)
		s.emit(ctx, m.ProjectID, "monitor_down", map[string]any{"monitor_id": m.ID, "monitor": m.Name, "error": r.Error})
	case before == "down" && after == "up":
		var d time.Duration
		s.db.QueryRow(ctx, `update monitor_outages set ended_at = now() where monitor_id = $1 and ended_at is null
			returning ended_at - started_at`, m.ID).Scan(&d)
		s.event(ctx, m.ProjectID, "", "info", "Monitor "+m.Name+" is back up after "+d.Round(time.Second).String())
		s.emit(ctx, m.ProjectID, "monitor_up", map[string]any{"monitor_id": m.ID, "monitor": m.Name, "downtime": d.Round(time.Second).String()})
	}
	s.hub.publish(m.ProjectID, "monitors", m.ID)
}

type monitorInput struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Target          string   `json:"target"`
	IntervalSeconds int      `json:"interval_seconds"`
	TimeoutMS       int      `json:"timeout_ms"`
	ExpectedStatus  string   `json:"expected_status"`
	Keyword         string   `json:"keyword"`
	ServerID        string   `json:"server_id"`
	Locations       []string `json:"locations"` // servers that also check it
	Paused          bool     `json:"paused"`
}

func (in *monitorInput) validate() error {
	in.Name, in.Target = strings.TrimSpace(in.Name), strings.TrimSpace(in.Target)
	if in.Name == "" || len(in.Name) > 80 {
		return errStatus(400, "name is required")
	}
	switch in.Type {
	case "http":
		if u, err := url.Parse(in.Target); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errStatus(400, "enter a URL starting with http:// or https://")
		}
		if in.ExpectedStatus == "" {
			in.ExpectedStatus = "2xx"
		}
		for _, p := range strings.Split(in.ExpectedStatus, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if !(len(p) == 3 && p[1:] == "xx" && p[0] >= '1' && p[0] <= '5') && !validStatusSpec(p) {
				return errStatus(400, "expected status must look like 2xx, 200 or 200-299")
			}
		}
	case "tcp":
		host, port, err := net.SplitHostPort(in.Target)
		if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
			return errStatus(400, "enter host:port, for example db.example.com:5432")
		}
		in.ExpectedStatus, in.Keyword = "2xx", ""
	default:
		return errStatus(400, "type must be http or tcp")
	}
	if in.IntervalSeconds == 0 {
		in.IntervalSeconds = 60
	}
	if in.TimeoutMS == 0 {
		in.TimeoutMS = 10000
	}
	if in.IntervalSeconds < 30 || in.IntervalSeconds > 3600 {
		return errStatus(400, "interval must be 30–3600 seconds")
	}
	if in.TimeoutMS < 1000 || in.TimeoutMS > 30000 {
		return errStatus(400, "timeout must be 1–30 seconds")
	}
	return nil
}

func validStatusSpec(p string) bool {
	lo, hi, isRange := strings.Cut(p, "-")
	a, err := strconv.Atoi(lo)
	if err != nil || a < 100 || a > 599 {
		return false
	}
	if isRange {
		b, err := strconv.Atoi(hi)
		return err == nil && b >= a && b <= 599
	}
	return true
}

const monitorColumns = `id, name, type, target, interval_seconds, timeout_ms, expected_status, keyword, server_id, locations, paused, status,
	last_checked_at, last_latency_ms, last_error, cert_expires_at, created_at,
	monitor_uptime(id, now() - interval '1 day') uptime_24h, monitor_uptime(id, now() - interval '30 days') uptime_30d`

func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select `+monitorColumns+` from monitors where project_id = $1 order by name`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getMonitor(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rng, step, _, err := rangeAndAgg(r)
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select `+monitorColumns+`,
		monitor_uptime(id, now() - interval '7 days') uptime_7d, monitor_uptime(id, now() - interval '90 days') uptime_90d,
		(select coalesce(json_agg(o order by o.started_at desc), '[]') from (select id, started_at, ended_at, error from monitor_outages
		   where monitor_id = m.id order by started_at desc limit 50) o) outages,
		(select json_build_object('t', coalesce(json_agg(extract(epoch from t)::bigint order by t), '[]'),
		   'latency', coalesce(json_agg(latency order by t), '[]'), 'failed', coalesce(json_agg(failed order by t), '[]'))
		 from (select date_bin($3::interval, ts, 'epoch') t, round(avg(latency_ms)) latency, count(*) filter (where not ok) failed
		   from monitor_checks where monitor_id = m.id and location is null and ts > now() - $4::interval group by 1) b) series,
		(select coalesce(json_agg(l order by l.name), '[]') from (select sv.id server_id, sv.name, c.ok, c.latency_ms, c.error, c.ts
		   from servers sv left join lateral (select ok, latency_ms, error, ts from monitor_checks
		     where monitor_id = m.id and location = sv.id order by ts desc limit 1) c on true where sv.id = any(m.locations)) l) location_results
		from monitors m where project_id = $1 and id::text = $2`, p.ID, r.PathValue("id"), step.String(), rng.String())
	if err == pgx.ErrNoRows {
		return errStatus(404, "monitor not found")
	} else if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) saveMonitor(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in monitorInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	if in.ServerID != "" {
		var ok bool
		s.db.QueryRow(r.Context(), `select exists(select 1 from servers where project_id = $1 and id::text = $2)`, p.ID, in.ServerID).Scan(&ok)
		if !ok {
			return errStatus(400, "server not found")
		}
	}
	if in.Locations == nil {
		in.Locations = []string{}
	}
	if len(in.Locations) > 10 {
		return errStatus(400, "check from at most 10 servers")
	}
	var found int
	s.db.QueryRow(r.Context(), `select count(*) from servers where project_id = $1 and id::text = any($2)`, p.ID, in.Locations).Scan(&found)
	if found != len(in.Locations) {
		return errStatus(400, "check locations must be servers in this project")
	}
	before := s.monitorLocations(r.Context(), r.PathValue("id"))
	args := []any{p.ID, in.Name, in.Type, in.Target, in.IntervalSeconds, in.TimeoutMS, in.ExpectedStatus, in.Keyword, nilIfEmpty(in.ServerID), in.Paused, in.Locations}
	var out json.RawMessage
	if id := r.PathValue("id"); id == "" {
		out, err = s.one(r.Context(), `insert into monitors (project_id, name, type, target, interval_seconds, timeout_ms, expected_status, keyword, server_id, paused, locations)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::uuid[]) returning id`, args...)
		s.auditProject(r, p, "monitor.create", in.Name, map[string]any{"target": in.Target})
	} else {
		// A changed target starts a fresh check cycle; pausing ends any open outage so it doesn't count as downtime.
		out, err = s.one(r.Context(), `update monitors set name = $2, type = $3, target = $4, interval_seconds = $5, timeout_ms = $6,
			expected_status = $7, keyword = $8, server_id = $9, paused = $10, locations = $11::uuid[], next_check_at = now(),
			status = case when $10 or target <> $4 then 'pending' else status end,
			fail_streak = case when $10 or target <> $4 then 0 else fail_streak end
			where project_id = $1 and id::text = $12 returning id`, append(args, id)...)
		if err == nil && in.Paused {
			s.db.Exec(r.Context(), `update monitor_outages set ended_at = now() where monitor_id::text = $1 and ended_at is null`, id)
		}
		s.auditProject(r, p, "monitor.update", in.Name, nil)
	}
	if err == pgx.ErrNoRows {
		return errStatus(404, "monitor not found")
	} else if err != nil {
		return err
	}
	s.pushMonitorLocations(r.Context(), append(before, in.Locations...))
	s.hub.publish(p.ID, "monitors", "")
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) deleteMonitor(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var name string
	var locations []string
	if err := s.db.QueryRow(r.Context(), `delete from monitors where project_id = $1 and id::text = $2 returning name, locations::text[]`,
		p.ID, r.PathValue("id")).Scan(&name, &locations); err != nil {
		return err
	}
	s.pushMonitorLocations(r.Context(), locations)
	s.auditProject(r, p, "monitor.delete", name, nil)
	s.hub.publish(p.ID, "monitors", "")
	w.WriteHeader(204)
	return nil
}

// checkMonitorNow runs a check immediately and returns its result.
func (s *Server) checkMonitorNow(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	m := monitor{ProjectID: p.ID}
	err = s.db.QueryRow(r.Context(), `select id::text, name, type, target, expected_status, keyword, timeout_ms from monitors where project_id = $1 and id::text = $2`,
		p.ID, r.PathValue("id")).Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.ExpectedStatus, &m.Keyword, &m.TimeoutMS)
	if err != nil {
		return err
	}
	res := s.check(r.Context(), m)
	s.recordCheck(r.Context(), m, res)
	writeJSON(w, 200, res)
	return nil
}

// vote combines a control-plane result with each location's latest one (no older than two intervals): the monitor
// fails only when more than half of the locations fail, so a network problem near one of them isn't an outage.
func (s *Server) vote(ctx context.Context, monitorID string, cp protocol.MonitorResult) protocol.MonitorResult {
	var total, failed int
	var lastErr string
	err := s.db.QueryRow(ctx, `select count(*), count(*) filter (where not x.ok), coalesce((array_agg(x.error) filter (where not x.ok))[1], '')
		from monitors m, lateral (select distinct on (c.location) c.ok, c.error from monitor_checks c
		  where c.monitor_id = m.id and c.location = any(m.locations) and c.ts > now() - make_interval(secs => 2 * m.interval_seconds + 30)
		  order by c.location, c.ts desc) x where m.id::text = $1`, monitorID).Scan(&total, &failed, &lastErr)
	if err != nil || total == 0 {
		return cp
	}
	if !cp.OK {
		failed++
		lastErr = cp.Error
	}
	total++
	r := cp
	if r.OK = failed*2 <= total; r.OK {
		r.Error = ""
	} else {
		r.Error = trunc(fmt.Sprintf("%s (failing from %d of %d locations)", lastErr, failed, total), 300)
	}
	return r
}

// storeLocationResults keeps results an agent reports for monitors that list its server as a location.
func (s *Server) storeLocationResults(ctx context.Context, ac *agentConn, b protocol.MonitorResults) error {
	for _, r := range b.Results {
		if _, err := s.db.Exec(ctx, `insert into monitor_checks (monitor_id, ts, ok, latency_ms, status_code, error, location)
			select m.id, least($3::timestamptz, now()), $4, $5, nullif($6, 0), $7, $2::uuid from monitors m
			where m.id::text = $1 and m.project_id::text = $8 and $2::uuid = any(m.locations)`,
			r.MonitorID, ac.serverID, r.TS, r.OK, r.LatencyMS, r.StatusCode, trunc(r.Error, 300), ac.projectID); err != nil {
			return err
		}
	}
	if len(b.Results) > 0 {
		s.hub.publish(ac.projectID, "monitors", "")
	}
	return nil
}

func (s *Server) monitorLocations(ctx context.Context, monitorID string) []string {
	var ids []string
	if monitorID != "" {
		s.db.QueryRow(ctx, `select locations::text[] from monitors where id::text = $1`, monitorID).Scan(&ids)
	}
	return ids
}

// pushMonitorLocations sends fresh config to servers whose list of monitors to check may have changed.
func (s *Server) pushMonitorLocations(ctx context.Context, serverIDs []string) {
	seen := map[string]bool{}
	for _, id := range serverIDs {
		if !seen[id] && s.hub.connected(id) {
			s.pushConfig(ctx, id)
		}
		seen[id] = true
	}
}
