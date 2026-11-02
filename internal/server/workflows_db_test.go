package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type wfFixture struct {
	s                                          *Server
	p                                          project
	saver, app, otherApp, monitor, otherServer string
}

func newWFFixture(t *testing.T) *wfFixture {
	s := testServer(t)
	f := &wfFixture{s: s}
	f.saver, _ = newTestUser(t, s, "saver", "x")
	f.p.OrgID = id(t, s, `insert into organizations (name) values ('wf') returning id::text`)
	f.p.ID = id(t, s, `insert into projects (org_id, name) values ($1,'wf') returning id::text`, f.p.OrgID)
	if _, err := s.db.Exec(context.Background(), `insert into memberships values ($1,$2,'admin')`, f.p.OrgID, f.saver); err != nil {
		t.Fatal(err)
	}
	f.app = id(t, s, `insert into applications (project_id, name, image) values ($1,'api','nginx') returning id::text`, f.p.ID)
	f.monitor = id(t, s, `insert into monitors (project_id, name, type, target) values ($1,'site','http','https://example.com') returning id::text`, f.p.ID)
	other := id(t, s, `insert into projects (org_id, name) values ($1,'other') returning id::text`, f.p.OrgID)
	f.otherApp = id(t, s, `insert into applications (project_id, name, image) values ($1,'api','nginx') returning id::text`, other)
	f.otherServer = id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'x','') returning id::text`, other)
	return f
}

func (f *wfFixture) workflow(t *testing.T, enabled bool, d wfDef) string {
	t.Helper()
	if err := f.s.validateWorkflow(context.Background(), f.p, f.saver, &d); err != nil {
		t.Fatal(err)
	}
	return id(t, f.s, `insert into workflows (project_id, name, enabled, trigger_type, definition, webhook_secret, saved_by)
		values ($1, 'wf', $2, $3, $4, 'sekrit', $5) returning id::text`, f.p.ID, enabled, d.Trigger.Type, d, f.saver)
}

// fakeSteps replaces the side-effecting step types with recorders; a step fails when its service id is "fail".
func fakeSteps(t *testing.T) func() []string {
	var mu sync.Mutex
	var calls []string
	for _, typ := range []string{"restart", "start", "stop", "redeploy", "alert", "http", "run_command"} {
		k := stepKinds[typ]
		orig := k.run
		k.run = func(x *wfExec, ctx context.Context, st wfStep) (string, error) {
			mu.Lock()
			calls = append(calls, typ+":"+x.expand(st.Message))
			mu.Unlock()
			if strings.Contains(st.Message, "fail") {
				return "", errors.New("boom")
			}
			return "done", nil
		}
		t.Cleanup(func() { k.run = orig })
	}
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
}

func waitRuns(t *testing.T, s *Server, wf string) []struct{ id, status, err string } {
	t.Helper()
	for range 100 {
		var running int
		s.db.QueryRow(context.Background(), `select count(*) from workflow_runs where workflow_id = $1 and status = 'running'`, wf).Scan(&running)
		if running == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	rows, err := s.db.Query(context.Background(), `select id::text, status, error from workflow_runs where workflow_id = $1 order by created_at`, wf)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct{ id, status, err string }
	for rows.Next() {
		var r struct{ id, status, err string }
		rows.Scan(&r.id, &r.status, &r.err)
		out = append(out, r)
	}
	return out
}

func stepLog(t *testing.T, s *Server, run string) []string {
	rows, _ := s.db.Query(context.Background(), `select path || ' ' || type || ' ' || status || ' ' || branch from workflow_run_steps where run_id = $1 order by id`, run)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		rows.Scan(&l)
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// step is a faked restart whose Message drives the fake (containing "fail" makes it fail).
func (f *wfFixture) step(msg string, onFail ...wfStep) wfStep {
	return wfStep{Type: "restart", ServiceID: f.app, Message: msg, OnFailure: onFail}
}

func TestWorkflowEmitScoping(t *testing.T) {
	f := newWFFixture(t)
	calls := fakeSteps(t)
	ctx := context.Background()
	steps := []wfStep{{Type: "restart", ServiceID: f.app, Message: "{{monitor}} {{data.error}}"}}
	scoped := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "monitor_down", MonitorID: f.monitor}, Steps: steps})
	anyMon := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "monitor_down"}, Steps: steps})
	off := f.workflow(t, false, wfDef{Trigger: wfTrigger{Type: "monitor_down"}, Steps: steps})
	otherKind := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "monitor_up"}, Steps: steps})

	f.s.emit(ctx, f.p.ID, "monitor_down", map[string]any{"monitor_id": "someone-else", "monitor": "x"})
	if r := waitRuns(t, f.s, scoped); len(r) != 0 {
		t.Fatalf("scoped workflow ran for another monitor: %v", r)
	}
	waitRuns(t, f.s, anyMon)
	f.s.emit(ctx, f.p.ID, "monitor_down", map[string]any{"monitor_id": f.monitor, "monitor": "site", "error": "timeout"})
	for wf, want := range map[string]int{scoped: 1, anyMon: 2, off: 0, otherKind: 0} {
		runs := waitRuns(t, f.s, wf)
		if len(runs) != want {
			t.Errorf("workflow got %d runs, want %d", len(runs), want)
		}
		for _, r := range runs {
			if r.status != "succeeded" {
				t.Errorf("run %s: %s", r.status, r.err)
			}
		}
	}
	got := strings.Join(calls(), ",")
	if strings.Count(got, "restart:site timeout") != 2 || strings.Count(got, "restart:") != 3 {
		t.Errorf("calls = %s", got)
	}
}

func TestWorkflowBranches(t *testing.T) {
	f := newWFFixture(t)
	calls := fakeSteps(t)
	wf := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "deploy_finished"}, Steps: []wfStep{
		{Type: "if", If: &wfCond{Type: "field_equals", Field: "data.status", Value: "failed"},
			Then: []wfStep{f.step("then {{service}}")}, Else: []wfStep{f.step("else")}},
		f.step("fail first", f.step("recover")),
		f.step("after"),
		f.step("fail again"),
		f.step("never"),
	}})
	if _, _, err := f.s.startRun(context.Background(), wf, "deploy_finished", map[string]any{"status": "failed", "service": "api"}, ""); err != nil {
		t.Fatal(err)
	}
	runs := waitRuns(t, f.s, wf)
	if len(runs) != 1 || runs[0].status != "failed" || !strings.Contains(runs[0].err, "boom") {
		t.Fatalf("runs = %+v", runs)
	}
	if got := strings.Join(calls(), ","); got != "restart:then api,restart:fail first,restart:recover,restart:after,restart:fail again" {
		t.Errorf("calls = %s", got)
	}
	want := []string{"0 if succeeded then", "0.then.0 restart succeeded", "1 restart failed on_failure", "1.on_failure.0 restart succeeded",
		"2 restart succeeded", "3 restart failed"}
	if got := stepLog(t, f.s, runs[0].id); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWorkflowRunsAsSaver(t *testing.T) {
	f := newWFFixture(t)
	calls := fakeSteps(t)
	ctx := context.Background()
	wf := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "restart", ServiceID: f.app}}})

	f.s.db.Exec(ctx, `update memberships set role = 'viewer' where user_id = $1`, f.saver)
	f.s.startRun(ctx, wf, "manual", nil, "")
	runs := waitRuns(t, f.s, wf)
	if runs[0].status != "failed" || !strings.Contains(runs[0].err, "can no longer operate") || len(calls()) != 0 {
		t.Fatalf("demoted saver: %+v, calls %v", runs, calls())
	}

	// run_command needs admin: a member saver gets as far as the step, which then fails.
	f.s.db.Exec(ctx, `update memberships set role = 'member' where user_id = $1`, f.saver)
	f.s.db.Exec(ctx, `update workflows set definition = $2 where id = $1`, wf,
		wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "restart", ServiceID: f.app}, {Type: "run_command", ServiceID: f.app, Command: "ls"}}})
	f.s.startRun(ctx, wf, "manual", nil, "")
	runs = waitRuns(t, f.s, wf)
	if runs[1].status != "failed" || !strings.Contains(runs[1].err, "needs the admin role") || len(calls()) != 1 {
		t.Fatalf("member saver: %+v, calls %v", runs, calls())
	}

	f.s.db.Exec(ctx, `delete from memberships where user_id = $1`, f.saver)
	f.s.startRun(ctx, wf, "manual", nil, "")
	if runs = waitRuns(t, f.s, wf); runs[2].status != "failed" {
		t.Fatalf("removed saver: %+v", runs)
	}
}

func TestWorkflowValidationRefs(t *testing.T) {
	f := newWFFixture(t)
	ctx := context.Background()
	for name, d := range map[string]wfDef{
		"service in another project": {Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "restart", ServiceID: f.otherApp}}},
		"server in another project":  {Trigger: wfTrigger{Type: "disk_full", ServerID: f.otherServer}, Steps: []wfStep{{Type: "wait", Seconds: 1}}},
		"push needs a git service":   {Trigger: wfTrigger{Type: "github_push", ServiceID: f.app}, Steps: []wfStep{{Type: "wait", Seconds: 1}}},
		"not a member":               {Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "alert", UserIDs: []string{newUUID()}}}},
		"commands need 2FA":          {Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "run_command", ServiceID: f.app, Command: "ls"}}},
	} {
		if err := f.s.validateWorkflow(ctx, f.p, f.saver, &d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	enableMFA(t, f.s, f.saver)
	d := wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "run_command", ServiceID: f.app, Command: "ls"},
		{Type: "alert", UserIDs: []string{f.saver}}}}
	if err := f.s.validateWorkflow(ctx, f.p, f.saver, &d); err != nil {
		t.Error(err)
	}
}

func TestWorkflowConcurrencyAndRecovery(t *testing.T) {
	f := newWFFixture(t)
	ctx := context.Background()
	release := make(chan struct{})
	k := stepKinds["restart"]
	orig := k.run
	k.run = func(*wfExec, context.Context, wfStep) (string, error) { <-release; return "", nil }
	t.Cleanup(func() { k.run = orig })
	wf := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "restart", ServiceID: f.app}}})
	if _, skipped, _ := f.s.startRun(ctx, wf, "manual", nil, ""); skipped {
		t.Fatal("first run skipped")
	}
	if _, skipped, _ := f.s.startRun(ctx, wf, "manual", nil, ""); !skipped {
		t.Fatal("second run should be skipped while the first is going")
	}
	close(release)
	if runs := waitRuns(t, f.s, wf); len(runs) != 2 || runs[1].status != "skipped" || runs[0].status != "succeeded" {
		t.Fatalf("runs = %+v", runs)
	}

	run := id(t, f.s, `insert into workflow_runs (workflow_id, trigger) values ($1, 'manual') returning id::text`, wf)
	f.s.db.Exec(ctx, `insert into workflow_run_steps (run_id, path, type, title) values ($1, '0', 'wait', 'Wait')`, run)
	f.s.recoverWorkflowRuns(ctx)
	var status, msg, stepStatus string
	f.s.db.QueryRow(ctx, `select r.status, r.error, s.status from workflow_runs r join workflow_run_steps s on s.run_id = r.id where r.id = $1`, run).
		Scan(&status, &msg, &stepStatus)
	if status != "failed" || msg != "Orbit restarted during this run" || stepStatus != "failed" {
		t.Errorf("recovered run: %s %q step %s", status, msg, stepStatus)
	}
}

func TestWorkflowWebhookAndSchedule(t *testing.T) {
	f := newWFFixture(t)
	calls := fakeSteps(t)
	ctx := context.Background()
	wf := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "webhook"}, Steps: []wfStep{f.step("ref={{data.ref}}")}})
	hook := func(secret, body string) int {
		r := httptest.NewRequest("POST", "/hooks/workflows/"+wf+"/"+secret, strings.NewReader(body))
		r.RemoteAddr = "203.0.113.9:1234"
		w := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if c := hook("wrong", `{}`); c != 404 {
		t.Errorf("wrong secret: %d", c)
	}
	if c := hook("sekrit", `[1]`); c != 400 {
		t.Errorf("non-object body: %d", c)
	}
	if c := hook("sekrit", `{"ref":"v1.2"}`); c != 202 {
		t.Errorf("valid call: %d", c)
	}
	waitRuns(t, f.s, wf)
	if got := strings.Join(calls(), ","); got != "restart:ref=v1.2" {
		t.Errorf("calls = %s", got)
	}

	sched := f.workflow(t, true, wfDef{Trigger: wfTrigger{Type: "schedule", Every: "hourly", At: "00:00"}, Steps: []wfStep{f.step("tick")}})
	f.s.db.Exec(ctx, `update workflows set last_scheduled_at = now() - interval '2 hours' where id = $1`, sched)
	f.s.workflowTick(ctx)
	f.s.workflowTick(ctx)
	if runs := waitRuns(t, f.s, sched); len(runs) != 1 || runs[0].status != "succeeded" {
		t.Errorf("scheduled runs = %+v", runs)
	}
}
