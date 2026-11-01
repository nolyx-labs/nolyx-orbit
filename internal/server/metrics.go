// Time-series queries for servers, containers and services.
package server

import (
	"fmt"
	"net/http"
	"time"
)

var ranges = map[string]time.Duration{
	"5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour,
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
}

// rangeAndAgg parses ?range= and ?agg= into a window, bucket size and a SQL aggregate template.
func rangeAndAgg(r *http.Request) (time.Duration, time.Duration, string, error) {
	rng, ok := ranges[r.URL.Query().Get("range")]
	if !ok {
		rng = time.Hour
	}
	step := max(10*time.Second, rng/120).Truncate(time.Second)
	aggs := map[string]string{
		"": "avg(%s)", "avg": "avg(%s)", "min": "min(%s)", "max": "max(%s)",
		"p95": "percentile_cont(0.95) within group (order by %s)", "p99": "percentile_cont(0.99) within group (order by %s)",
	}
	agg, ok := aggs[r.URL.Query().Get("agg")]
	if !ok {
		return 0, 0, "", errStatus(400, "agg must be avg, min, max, p95 or p99")
	}
	return rng, step, agg, nil
}

// metricsSQL returns columnar arrays (uPlot's native format). Network is summed across servers.
func metricsSQL(agg, where string) string {
	a := func(expr string) string { return fmt.Sprintf(agg, expr) }
	return fmt.Sprintf(`
		with b as (
		  select date_bin($2::interval, ts, 'epoch') as t, server_id,
		    %s as cpu, %s as mem, %s as disk, %s as rx, %s as tx, %s as load, %s as io_read, %s as io_write, %s as io_util
		  from server_metrics where %s and ts > now() - $3::interval group by 1, 2
		), agg as (
		  select t, avg(cpu) cpu, avg(mem) mem, avg(disk) disk, sum(rx) rx, sum(tx) tx, avg(load) load,
		    sum(io_read) io_read, sum(io_write) io_write, max(io_util) io_util from b group by t order by t
		)
		select coalesce(json_agg(extract(epoch from t)::bigint order by t), '[]') t,
		  coalesce(json_agg(round(cpu::numeric, 2) order by t), '[]') cpu, coalesce(json_agg(round(mem::numeric, 2) order by t), '[]') mem,
		  coalesce(json_agg(round(disk::numeric, 2) order by t), '[]') disk, coalesce(json_agg(round(rx) order by t), '[]') rx,
		  coalesce(json_agg(round(tx) order by t), '[]') tx, coalesce(json_agg(round(load::numeric, 2) order by t), '[]') load,
		  coalesce(json_agg(round(io_read) order by t), '[]') io_read, coalesce(json_agg(round(io_write) order by t), '[]') io_write,
		  coalesce(json_agg(round(io_util::numeric, 1) order by t), '[]') io_util
		from agg`,
		a("cpu"), a("mem_used::float8 / nullif(mem_total,0) * 100"), a("disk_used::float8 / nullif(disk_total,0) * 100"),
		a("net_rx"), a("net_tx"), a("load1"), a("io_read"), a("io_write"), a("io_util"), where)
}

func (s *Server) projectMetrics(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rng, step, agg, err := rangeAndAgg(r)
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), metricsSQL(agg, "server_id in (select id from servers where project_id = $1)"), p.ID, step.String(), rng.String())
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) serverMetrics(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rng, step, agg, err := rangeAndAgg(r)
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), metricsSQL(agg, "server_id = (select id from servers where id::text = $4 and project_id = $1)"),
		p.ID, step.String(), rng.String(), r.PathValue("sid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) containerMetrics(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rng, step, agg, err := rangeAndAgg(r)
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), fmt.Sprintf(`
		with agg as (
		  select date_bin($2::interval, ts, 'epoch') t, %s cpu, %s mem from container_metrics
		  where server_id = (select id from servers where id::text = $4 and project_id = $1) and docker_id = $5
		    and ts > now() - $3::interval group by 1
		)
		select coalesce(json_agg(extract(epoch from t)::bigint order by t), '[]') t,
		  coalesce(json_agg(round(cpu::numeric, 2) order by t), '[]') cpu, coalesce(json_agg(round(mem) order by t), '[]') mem from agg`,
		fmt.Sprintf(agg, "cpu"), fmt.Sprintf(agg, "mem_used")), p.ID, step.String(), rng.String(), r.PathValue("sid"), r.PathValue("cid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// appMetrics returns request rate (req/s), error rate (%) and p95 latency for proxied apps.
func (s *Server) appMetrics(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rng, step, _, err := rangeAndAgg(r)
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `
		with agg as (
		  select date_bin($2::interval, ts, 'epoch') t, sum(requests) req, sum(errors) err, max(p95_ms) p95
		  from app_metrics where application_id in (select id from applications where project_id = $1 and ($4 = '' or id::text = $4))
		    and ts > now() - $3::interval group by 1
		)
		select coalesce(json_agg(extract(epoch from t)::bigint order by t), '[]') t,
		  coalesce(json_agg(round((req / extract(epoch from $2::interval))::numeric, 2) order by t), '[]') rps,
		  coalesce(json_agg(round((err * 100.0 / nullif(req, 0))::numeric, 2) order by t), '[]') error_rate,
		  coalesce(json_agg(round(p95::numeric, 1) order by t), '[]') p95
		from agg`, p.ID, step.String(), rng.String(), r.URL.Query().Get("app"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
