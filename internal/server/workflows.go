// Workflows: the event bus, runs and their executor, the scheduler, webhooks and the API.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// emit starts a run of every enabled workflow in the project whose trigger is kind and whose scope matches data.
func (s *Server) emit(ctx context.Context, projectID, kind string, data map[string]any) {
	rows, err := s.db.Query(ctx, `select id::text, definition from workflows where project_id::text = $1 and enabled and trigger_type = $2`, projectID, kind)
	if err != nil {
		s.log.Error("workflow emit", "kind", kind, "err", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		var d wfDef
		if rows.Scan(&id, &d) == nil && d.Trigger.matches(data) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, _, err := s.startRun(ctx, id, kind, data, ""); err != nil {
			s.log.Error("workflow start", "workflow", id, "err", err)
		}
	}
}

// startRun records a run and executes it in the background. If the workflow already has a run going,
// the new one is recorded as skipped so storms of events can't pile up runs.
func (s *Server) startRun(ctx context.Context, workflowID, kind string, data map[string]any, startedBy string) (runID string, skipped bool, err error) {
	if data == nil {
		data = map[string]any{}
	}
	err = s.db.QueryRow(ctx, `insert into workflow_runs (workflow_id, trigger, data, started_by) values ($1, $2, $3, nullif($4,'')::uuid)
		on conflict (workflow_id) where status = 'running' do nothing returning id::text`, workflowID, kind, data, startedBy).Scan(&runID)
	if err == pgx.ErrNoRows {
		skipped = true
		err = s.db.QueryRow(ctx, `insert into workflow_runs (workflow_id, trigger, data, started_by, status, error, finished_at)
			values ($1, $2, $3, nullif($4,'')::uuid, 'skipped', 'Skipped: the previous run was still going', now()) returning id::text`,
			workflowID, kind, data, startedBy).Scan(&runID)
	}
	if err != nil {
		return "", false, err
	}
	if !skipped {
		go s.execRun(context.WithoutCancel(ctx), runID)
	}
	var pid string
	s.db.QueryRow(ctx, `select project_id::text from workflows where id = $1`, workflowID).Scan(&pid)
	s.hub.publish(pid, "workflows", workflowID)
	return runID, skipped, nil
}

// wfExec is one run in progress. Actions run as the user who last saved the workflow, with their current role.
type wfExec struct {
	s                         *Server
	p                         project
	runID, wfID, wfName, kind string
	saverID, saverEmail       string
	data                      map[string]any
	vars                      map[string]string
}

func (x *wfExec) expand(s string) string { return expand(s, x.vars, x.data) }

func (s *Server) execRun(ctx context.Context, runID string) {
	x := &wfExec{s: s, runID: runID}
	var def wfDef
	var saver *string
	err := s.db.QueryRow(ctx, `select w.id::text, w.name, w.project_id::text, w.definition, w.saved_by::text, coalesce(u.email, ''), r.trigger, r.data
		from workflow_runs r join workflows w on w.id = r.workflow_id left join users u on u.id = w.saved_by where r.id = $1`, runID).
		Scan(&x.wfID, &x.wfName, &x.p.ID, &def, &saver, &x.saverEmail, &x.kind, &x.data)
	if err != nil {
		s.log.Error("workflow run", "run", runID, "err", err)
		return
	}
	str := func(k string) string { v, _ := x.data[k].(string); return v }
	x.vars = map[string]string{"service": str("service"), "server": str("server"), "monitor": str("monitor"),
		"trigger": triggerLabels[x.kind], "run_url": s.cfg.AppURL + "/p/" + x.p.ID + "/workflows/" + x.wfID + "/runs/" + runID}
	if saver == nil {
		err = errors.New("the person who saved this workflow no longer exists: save it again")
	} else {
		x.saverID = *saver
		// Re-check the saver's access now, through the same tenancy boundary as API requests.
		r, _ := http.NewRequestWithContext(context.WithValue(ctx, userKey, user{ID: x.saverID, Email: x.saverEmail}), "GET", "/", nil)
		r.SetPathValue("pid", x.p.ID)
		var p project
		if p, err = s.projectAccess(r, "member"); err != nil {
			err = fmt.Errorf("%s, who last saved this workflow, can no longer operate this project (%v)", x.saverEmail, err)
		} else {
			x.p = p
			err = x.run(ctx, def.Steps, "")
		}
	}
	status, msg := "succeeded", ""
	if err != nil {
		status, msg = "failed", trunc(err.Error(), 2000)
	}
	s.db.Exec(ctx, `update workflow_runs set status = $2, error = $3, finished_at = now() where id = $1`, runID, status, msg)
	s.hub.publish(x.p.ID, "workflows", x.wfID)
	if err != nil && x.kind != "manual" {
		s.wfNotify(ctx, x.p.ID, true, nil, nil, x.wfName, fmt.Sprintf("Workflow %s failed: %s", x.wfName, msg), x.vars["trigger"], x.vars["run_url"])
	}
}

func (x *wfExec) run(ctx context.Context, steps []wfStep, prefix string) error {
	for i, st := range steps {
		if err := x.step(ctx, st, prefix+strconv.Itoa(i)); err != nil {
			return err
		}
	}
	return nil
}

// step runs one step and whatever branch it leads to. An error stops the whole run.
func (x *wfExec) step(ctx context.Context, st wfStep, path string) error {
	k := stepKinds[st.Type]
	title := x.title(ctx, st, k)
	var rec int64
	if err := x.s.db.QueryRow(ctx, `insert into workflow_run_steps (run_id, path, type, title) values ($1,$2,$3,$4) returning id`,
		x.runID, path, st.Type, title).Scan(&rec); err != nil {
		return err
	}
	x.s.hub.publish(x.p.ID, "workflows", x.wfID)
	finish := func(status, branch, output, errMsg string) {
		x.s.db.Exec(ctx, `update workflow_run_steps set status = $2, branch = $3, output = $4, error = $5, finished_at = now() where id = $1`,
			rec, status, branch, trunc(output, 20000), trunc(errMsg, 4000))
		x.s.hub.publish(x.p.ID, "workflows", x.wfID)
	}
	if st.Type == "if" {
		ok, err := x.cond(ctx, *st.If)
		branch, next, out := "then", st.Then, "The condition was true"
		if !ok {
			branch, next, out = "else", st.Else, "The condition was false"
		}
		if err != nil {
			out = "Couldn't check the condition, so it counts as false: " + err.Error()
		}
		finish("succeeded", branch, out, "")
		return x.run(ctx, next, path+"."+branch+".")
	}
	var out string
	var err error
	if roleRank[x.p.Role] < roleRank[k.role] {
		err = fmt.Errorf("%s needs the %s role in this project to do this", x.saverEmail, k.role)
	} else {
		out, err = k.run(x, ctx, st)
	}
	if err == nil {
		finish("succeeded", "", out, "")
		return nil
	}
	if len(st.OnFailure) == 0 {
		finish("failed", "", out, err.Error())
		return fmt.Errorf("%s: %v", title, err)
	}
	finish("failed", "on_failure", out, err.Error())
	return x.run(ctx, st.OnFailure, path+".on_failure.")
}

// title describes a step for the run log, naming what it acts on as it is now.
func (x *wfExec) title(ctx context.Context, st wfStep, k *stepKind) string {
	name := func(sql, id string) string {
		var n string
		if x.s.db.QueryRow(ctx, sql, x.p.ID, id).Scan(&n) != nil {
			return "(deleted)"
		}
		return n
	}
	switch {
	case st.ServiceID != "":
		return k.label + " " + name(`select name from applications where project_id::text = $1 and id::text = $2`, st.ServiceID)
	case st.MonitorID != "":
		return k.label + " " + name(`select name from monitors where project_id::text = $1 and id::text = $2`, st.MonitorID)
	case st.Type == "wait":
		return fmt.Sprintf("Wait %s", time.Duration(st.Seconds)*time.Second)
	case st.Type == "http":
		return k.label + " " + st.Method + " " + trunc(st.URL, 200)
	}
	return k.label
}

func (x *wfExec) cond(ctx context.Context, c wfCond) (bool, error) {
	var ok bool
	var err error
	switch c.Type {
	case "field_equals", "field_contains":
		return fieldCond(c, x.vars, x.data), nil
	case "time_between":
		return timeBetween(c.From, c.To, time.Now()), nil
	case "monitor_up":
		err = x.s.db.QueryRow(ctx, `select status = 'up' and not paused from monitors where project_id::text = $1 and id::text = $2`, x.p.ID, c.MonitorID).Scan(&ok)
	case "service_running":
		err = x.s.db.QueryRow(ctx, `select exists(select 1 from applications a join containers c on c.server_id = a.server_id and c.labels->>'orbit.app' = a.name
			where a.project_id::text = $1 and a.id::text = $2 and c.state = 'running')`, x.p.ID, c.ServiceID).Scan(&ok)
	case "disk_below":
		err = x.s.db.QueryRow(ctx, `select m.disk_used::float8 / nullif(m.disk_total, 0) * 100 < $3 from server_metrics m join servers s on s.id = m.server_id
			where s.project_id::text = $1 and s.id::text = $2 and m.ts > now() - interval '10 minutes' order by m.ts desc limit 1`, x.p.ID, c.ServerID, c.Percent).Scan(&ok)
		if err == pgx.ErrNoRows {
			err = errors.New("no recent disk usage from that server")
		}
	}
	return ok, err
}

// recoverWorkflowRuns fails runs that were in flight when the control plane stopped; their goroutines are gone.
func (s *Server) recoverWorkflowRuns(ctx context.Context) {
	if _, err := s.db.Exec(ctx, `with r as (update workflow_runs set status = 'failed', error = 'Orbit restarted during this run', finished_at = now()
		where status = 'running' returning id) update workflow_run_steps set status = 'failed', error = 'Orbit restarted during this step', finished_at = now()
		where status = 'running' and run_id in (select id from r)`); err != nil {
		s.log.Error("recover workflow runs", "err", err)
	}
}

// workflowTick runs due schedules and fires "disk nearly full". Called by the worker.
func (s *Server) workflowTick(ctx context.Context) {
	now := time.Now()
	rows, err := s.db.Query(ctx, `select id::text, definition, last_scheduled_at from workflows where enabled and trigger_type = 'schedule'`)
	if err != nil {
		s.log.Error("workflow schedules", "err", err)
		return
	}
	type due struct {
		id         string
		slot, last time.Time
	}
	var list []due
	for rows.Next() {
		var d due
		var def wfDef
		if rows.Scan(&d.id, &def, &d.last) == nil {
			if d.slot = scheduleSlot(def.Trigger, now); d.slot.After(d.last) {
				list = append(list, d)
			}
		}
	}
	rows.Close()
	for _, d := range list {
		// Claim the slot so a slow tick can't start it twice.
		tag, err := s.db.Exec(ctx, `update workflows set last_scheduled_at = $2 where id = $1 and last_scheduled_at = $3`, d.id, d.slot, d.last)
		if err == nil && tag.RowsAffected() == 1 {
			s.startRun(ctx, d.id, "schedule", map[string]any{"scheduled_for": d.slot}, "")
		}
	}

	// Below 85% clears the mark, so usage hovering around 90% doesn't fire on every sample.
	full, err := s.db.Query(ctx, `with latest as (
		  select distinct on (server_id) server_id, disk_used::float8 / nullif(disk_total, 0) * 100 as pct
		  from server_metrics where ts > now() - interval '5 minutes' order by server_id, ts desc),
		cleared as (delete from workflow_disk_full d using latest l where l.server_id = d.server_id and l.pct < 85),
		marked as (insert into workflow_disk_full select server_id from latest where pct >= 90 on conflict do nothing returning server_id)
		select s.project_id::text, s.id::text, s.name, l.pct from marked m join servers s on s.id = m.server_id join latest l on l.server_id = m.server_id`)
	if err != nil {
		s.log.Error("workflow disk check", "err", err)
		return
	}
	type hit struct {
		project, id, name string
		pct               float64
	}
	var hits []hit
	for full.Next() {
		var h hit
		full.Scan(&h.project, &h.id, &h.name, &h.pct)
		hits = append(hits, h)
	}
	full.Close()
	for _, h := range hits {
		s.emit(ctx, h.project, "disk_full", map[string]any{"server_id": h.id, "server": h.name, "percent": int(h.pct)})
	}
}

// restartTimes holds recent restart times per container, for "a service keeps restarting". In memory: a restart
// of the control plane only resets the window.
var restartTimes = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

// containerRestarted fires service_restarting once a service's container has restarted 3 times within 15 minutes.
func (s *Server) containerRestarted(ctx context.Context, ac *agentConn, dockerID, app string, restarts int) {
	if app == "" || restarts <= 0 {
		return
	}
	key, now := ac.serverID+"/"+dockerID, time.Now()
	restartTimes.Lock()
	var recent []time.Time
	for _, t := range restartTimes.m[key] {
		if now.Sub(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	for range min(restarts, 3) {
		recent = append(recent, now)
	}
	fire := len(recent) >= 3
	if fire {
		recent = nil
	}
	restartTimes.m[key] = recent
	restartTimes.Unlock()
	if fire {
		s.emitService(ctx, ac, app, "service_restarting", false)
	}
}

// containerStopped fires service_stopped when a service's container exits without anyone asking:
// no stop/restart command and no deploy for it just now.
func (s *Server) containerStopped(ctx context.Context, ac *agentConn, dockerID, app, state string) {
	if app == "" || (state != "exited" && state != "dead") {
		return
	}
	var asked bool
	s.db.QueryRow(ctx, `select exists(select 1 from commands where server_id::text = $1 and args->>'container_id' = $2
		and action in ('container.stop','container.restart','container.remove') and created_at > now() - interval '2 minutes')`,
		ac.serverID, dockerID).Scan(&asked)
	if !asked {
		s.emitService(ctx, ac, app, "service_stopped", true)
	}
}

func (s *Server) emitService(ctx context.Context, ac *agentConn, app, kind string, skipDeploying bool) {
	var id, server string
	var deploying bool
	err := s.db.QueryRow(ctx, `select a.id::text, s.name, exists(select 1 from deployments d where d.application_id = a.id
		  and (d.status = 'running' or d.finished_at > now() - interval '2 minutes'))
		from applications a join servers s on s.id = a.server_id where a.project_id::text = $1 and a.server_id::text = $2 and a.name = $3`,
		ac.projectID, ac.serverID, app).Scan(&id, &server, &deploying)
	if err != nil || (skipDeploying && deploying) {
		return
	}
	s.emit(ctx, ac.projectID, kind, map[string]any{"service_id": id, "service": app, "server_id": ac.serverID, "server": server})
}

// emitPush fires github_push for a verified push webhook to a service.
func (s *Server) emitPush(ctx context.Context, projectID, appID, app string, body []byte) {
	var push struct {
		Ref, After string
		Deleted    bool
		HeadCommit *struct {
			Message string
			Author  struct{ Name string }
		} `json:"head_commit"`
		Pusher struct{ Name string }
	}
	if json.Unmarshal(body, &push) != nil {
		return
	}
	branch, ok := strings.CutPrefix(push.Ref, "refs/heads/")
	if !ok || push.Deleted || push.HeadCommit == nil {
		return
	}
	var repo string
	s.db.QueryRow(ctx, `select repo_url from applications where id::text = $1`, appID).Scan(&repo)
	s.emit(ctx, projectID, "github_push", map[string]any{"service_id": appID, "service": app, "repo": repo, "branch": branch,
		"commit": push.After, "message": strings.SplitN(push.HeadCommit.Message, "\n", 2)[0], "author": push.HeadCommit.Author.Name, "pusher": push.Pusher.Name})
}

// workflowWebhook starts a webhook-triggered workflow. The JSON body is available to steps as {{data.field}}.
func (s *Server) workflowWebhook(w http.ResponseWriter, r *http.Request) error {
	ipKey, wfKey := "wfhook-ip:"+clientIP(r), "wfhook:"+r.PathValue("id")
	if s.attempts.blocked(ipKey, 20) || s.attempts.blocked(wfKey, 120) {
		return errStatus(429, "too many requests, try again later")
	}
	var secret, projectID string
	err := s.db.QueryRow(r.Context(), `select webhook_secret, project_id::text from workflows where id::text = $1 and enabled and trigger_type = 'webhook'`,
		r.PathValue("id")).Scan(&secret, &projectID)
	if err != nil || subtle.ConstantTimeCompare([]byte(secret), []byte(r.PathValue("secret"))) != 1 {
		s.attempts.fail(ipKey)
		return errStatus(404, "not found")
	}
	s.attempts.fail(wfKey) // counts every accepted call: at most 120 runs per 15 minutes
	data := map[string]any{}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(body) > 64<<10 {
		return errStatus(413, "body too large (64 KB)")
	}
	if strings.TrimSpace(string(body)) != "" && json.Unmarshal(body, &data) != nil {
		return errStatus(400, "the body must be a JSON object")
	}
	id, skipped, err := s.startRun(r.Context(), r.PathValue("id"), "webhook", data, "")
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"run_id": id, "skipped": skipped})
	return nil
}

// ---- API ----

type workflowInput struct {
	Name       string `json:"name"`
	Definition wfDef  `json:"definition"`
}

func (s *Server) decodeWorkflow(r *http.Request, p project) (workflowInput, error) {
	var in workflowInput
	if err := decode(r, &in); err != nil {
		return in, err
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" || len(in.Name) > 80 {
		return in, errStatus(400, "name is required (up to 80 characters)")
	}
	return in, s.validateWorkflow(r.Context(), p, currentUser(r).ID, &in.Definition)
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select w.id, w.name, w.enabled, w.definition->'trigger' as trigger, w.updated_at,
		(select row_to_json(x) from (select id, status, error, created_at, finished_at from workflow_runs
		   where workflow_id = w.id order by created_at desc limit 1) x) as last_run
		from workflows w where w.project_id = $1 order by w.name`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	// The webhook URL carries its secret, so only admins (who can edit the workflow) see it.
	out, err := s.one(r.Context(), `select w.id, w.name, w.enabled, w.definition, w.updated_at, u.email as saved_by,
		case when $3 and w.trigger_type = 'webhook' then $4 || '/hooks/workflows/' || w.id || '/' || w.webhook_secret end as webhook_url
		from workflows w left join users u on u.id = w.saved_by where w.project_id = $1 and w.id::text = $2`,
		p.ID, r.PathValue("id"), roleRank[p.Role] >= roleRank["admin"], s.cfg.AppURL)
	if err == pgx.ErrNoRows {
		return errStatus(404, "workflow not found")
	} else if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	in, err := s.decodeWorkflow(r, p)
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `insert into workflows (project_id, name, trigger_type, definition, webhook_secret, saved_by)
		values ($1, $2, $3, $4, $5, $6) returning id`, p.ID, in.Name, in.Definition.Trigger.Type, in.Definition, randomSecret(), currentUser(r).ID)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "workflow.create", in.Name, map[string]any{"trigger": in.Definition.Trigger.Type})
	s.hub.publish(p.ID, "workflows", "")
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) updateWorkflow(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	in, err := s.decodeWorkflow(r, p)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `update workflows set name = $3, trigger_type = $4, definition = $5, saved_by = $6, updated_at = now(),
		last_scheduled_at = now() where project_id = $1 and id::text = $2`, p.ID, r.PathValue("id"), in.Name, in.Definition.Trigger.Type, in.Definition, currentUser(r).ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errStatus(404, "workflow not found")
	}
	s.auditProject(r, p, "workflow.update", in.Name, map[string]any{"trigger": in.Definition.Trigger.Type})
	s.hub.publish(p.ID, "workflows", r.PathValue("id"))
	return s.getWorkflow(w, r)
}

func (s *Server) setWorkflowEnabled(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	// Turning a schedule on starts counting from now rather than catching up on missed slots.
	var name string
	if err := s.db.QueryRow(r.Context(), `update workflows set enabled = $3, last_scheduled_at = case when $3 and not enabled then now() else last_scheduled_at end
		where project_id = $1 and id::text = $2 returning name`, p.ID, r.PathValue("id"), in.Enabled).Scan(&name); err != nil {
		return err
	}
	action := "workflow.disable"
	if in.Enabled {
		action = "workflow.enable"
	}
	s.auditProject(r, p, action, name, nil)
	s.hub.publish(p.ID, "workflows", r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"enabled": in.Enabled})
	return nil
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from workflows where project_id = $1 and id::text = $2 returning name`, p.ID, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "workflow.delete", name, nil)
	s.hub.publish(p.ID, "workflows", "")
	w.WriteHeader(204)
	return nil
}

func (s *Server) runWorkflow(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from workflows where project_id = $1 and id::text = $2`, p.ID, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "workflow.run", name, nil)
	id, skipped, err := s.startRun(r.Context(), r.PathValue("id"), "manual", nil, currentUser(r).ID)
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"id": id, "skipped": skipped})
	return nil
}

const workflowRunSQL = `select r.id, r.status, r.trigger, r.error, r.created_at, r.finished_at, u.email as started_by,
	extract(epoch from coalesce(r.finished_at, now()) - r.created_at)::int as duration
	from workflow_runs r join workflows w on w.id = r.workflow_id left join users u on u.id = r.started_by`

func (s *Server) listWorkflowRuns(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), workflowRunSQL+` where w.project_id = $1 and w.id::text = $2 order by r.created_at desc limit 100`, p.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getWorkflowRun(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select x.*, d.data, (select coalesce(json_agg(st order by st.id), '[]') from (
		  select id, path, type, title, status, branch, output, error, started_at, finished_at,
		    extract(epoch from coalesce(finished_at, now()) - started_at)::int as duration
		  from workflow_run_steps where run_id = x.id) st) as steps
		from (`+workflowRunSQL+` where w.project_id = $1 and w.id::text = $2 and r.id::text = $3) x join workflow_runs d on d.id = x.id`,
		p.ID, r.PathValue("id"), r.PathValue("rid"))
	if err == pgx.ErrNoRows {
		return errStatus(404, "run not found")
	} else if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
