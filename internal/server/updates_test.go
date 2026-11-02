package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"orbit/internal/protocol"
	"strings"
	"sync"
	"testing"
	"time"
)

// updatesFixture: a project with an owner and servers named after their keys, each with one pending update.
func updatesFixture(t *testing.T, s *Server, servers map[string]string) (string, string, map[string]string) {
	ctx := context.Background()
	uid, _ := newTestUser(t, s, "owner", "x")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	s.db.Exec(ctx, `insert into memberships values ($1,$2,'owner')`, org, uid)
	ids := map[string]string{}
	for name, status := range servers {
		ids[name] = id(t, s, `insert into servers (project_id, name, agent_secret_hash, status) values ($1,$2,'x',$3) returning id::text`, p, name, status)
		pkg := protocol.OSPackage{Name: "openssl", Installed: "1", Available: "2", Security: true}
		if name == "plain" {
			pkg = protocol.OSPackage{Name: "tzdata", Installed: "1", Available: "2"}
		}
		s.storeCheck(ctx, ids[name], protocol.OSCheckResult{Manager: "apt", Packages: []protocol.OSPackage{pkg}})
	}
	return p, uid, ids
}

func rolloutState(t *testing.T, s *Server, rid string) (string, map[string]string) {
	t.Helper()
	var status string
	s.db.QueryRow(context.Background(), `select status from os_rollouts where id::text = $1`, rid).Scan(&status)
	rows, _ := s.db.Query(context.Background(), `select sv.name, rs.status || ': ' || rs.result from os_rollout_servers rs
		join servers sv on sv.id = rs.server_id where rs.rollout_id::text = $1`, rid)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, st string
		rows.Scan(&n, &st)
		out[n] = st
	}
	return status, out
}

func TestRolloutBatches(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	p, uid, ids := updatesFixture(t, s, map[string]string{"a": "online", "b": "online", "c": "offline", "d": "online", "e": "online", "plain": "online"})
	names := map[string]string{}
	for n, i := range ids {
		names[i] = n
	}

	// Security-only targets the servers with a security update waiting, not "plain".
	dry, err := s.list(ctx, rolloutTargetsSQL, rolloutInput{Scope: "security"}.targetArgs(p)...)
	if err != nil || strings.Contains(string(dry), "plain") || !strings.Contains(string(dry), `"e"`) {
		t.Fatalf("dry run: %s %v", dry, err)
	}

	var mu sync.Mutex
	var inFlight, maxInFlight int
	var calls []string
	fake := func(fail string, rebooting bool) installFunc {
		return func(ctx context.Context, _, serverID, opID string, args protocol.OSInstallArgs) (protocol.OSInstallResult, error) {
			mu.Lock()
			inFlight++
			maxInFlight = max(maxInFlight, inFlight)
			calls = append(calls, names[serverID])
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			if opID == "" || args.Scope != "security" || args.Reboot != "if_required" {
				t.Errorf("install args: %q %+v", opID, args)
			}
			if names[serverID] == fail {
				return protocol.OSInstallResult{}, errors.New("dpkg was interrupted")
			}
			// Still waiting afterwards, so the next rollout targets the same servers.
			return protocol.OSInstallResult{Rebooting: rebooting, Check: protocol.OSCheckResult{Packages: []protocol.OSPackage{{Name: "openssl", Security: true}}}}, nil
		}
	}
	run := func(in rolloutInput, install installFunc) string {
		t.Helper()
		calls, maxInFlight = nil, 0
		rid, err := s.startRollout(ctx, p, uid, in)
		if err != nil {
			t.Fatal(err)
		}
		s.runRollout(ctx, rid, install)
		return rid
	}

	// Batches of two, in name order: a+b, then c (offline: skipped) + d, and b fails so it stops there.
	rid := run(rolloutInput{Scope: "security", Reboot: "if_required", BatchSize: 2, StopOnFailure: true}, fake("b", false))
	status, got := rolloutState(t, s, rid)
	want := map[string]string{"a": "done: Updates installed", "b": "failed: dpkg was interrupted",
		"c": "skipped: Not started because an earlier server failed", "d": "skipped: Not started because an earlier server failed",
		"e": "skipped: Not started because an earlier server failed"}
	if status != "failed" || !mapsEqual(got, want) || maxInFlight != 2 {
		t.Errorf("stop on failure: %s %v (max in flight %d)", status, got, maxInFlight)
	}

	// Without stopping: the offline server is skipped when its turn comes, the rest carry on.
	rid = run(rolloutInput{Scope: "security", Reboot: "if_required", BatchSize: 1}, fake("b", true))
	status, got = rolloutState(t, s, rid)
	if status != "failed" || got["c"] != "skipped: Server was offline" || got["d"] != "done: Updates installed, restarting the server" ||
		maxInFlight != 1 || strings.Join(calls, "") != "abde" {
		t.Errorf("carry on: %s %v %v", status, got, calls)
	}
	// A server restarting for updates holds its alerts.
	var held bool
	s.db.QueryRow(ctx, `select exists(select 1 from (`+maintenanceSQL+`) h(id) where id = $1)`, ids["d"]).Scan(&held)
	if !held {
		t.Error("alerts not held while the server restarts")
	}

	// Cancelling stops servers that haven't started.
	cancelOnFirst := func(ctx context.Context, _, serverID, _ string, _ protocol.OSInstallArgs) (protocol.OSInstallResult, error) {
		s.db.Exec(ctx, `update os_rollouts set status = 'cancelled' where id in (select rollout_id from os_rollout_servers where server_id::text = $1)`, serverID)
		return protocol.OSInstallResult{Check: protocol.OSCheckResult{Packages: []protocol.OSPackage{{Name: "openssl", Security: true}}}}, nil
	}
	rid = run(rolloutInput{Scope: "security", Reboot: "never", BatchSize: 1}, cancelOnFirst)
	status, got = rolloutState(t, s, rid)
	if status != "cancelled" || got["a"] != "done: Updates installed" || got["b"] != "skipped: Rollout cancelled" {
		t.Errorf("cancel: %s %v", status, got)
	}

	// Specific packages: a server where none of them are waiting is skipped; exact versions are always sent.
	if pk := serverPackages([]protocol.OSPackageSpec{{Name: "openssl"}, {Name: "tzdata", Version: "2023c"}, {Name: "curl"}},
		[]protocol.OSPackage{{Name: "openssl"}}); len(pk) != 2 || pk[0].Name != "openssl" || pk[1].Version != "2023c" {
		t.Errorf("serverPackages: %+v", pk)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestMaintenanceWindows(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	p, uid, ids := updatesFixture(t, s, map[string]string{"web": "online", "db": "online"})
	mon := id(t, s, `insert into monitors (project_id, name, type, target, server_id) values ($1,'api','tcp','x:1',$2) returning id::text`, p, ids["web"])
	page := id(t, s, `insert into status_pages (project_id, name, slug) values ($1,'Acme','acme-`+newUUID()[:8]+`') returning id::text`, p)
	comp := id(t, s, `insert into status_components (page_id, name, source_type, source_id) values ($1,'API','monitor',$2) returning id::text`, page, mon)

	create := func(server, body string) (int, string) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("pid", p)
		r.SetPathValue("sid", ids[server])
		w := httptest.NewRecorder()
		s.h(s.createMaintenance)(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid})))
		return w.Code, w.Body.String()
	}
	held := func(target string) (h bool) {
		s.db.QueryRow(ctx, `select exists(select 1 from (`+maintenanceSQL+`) h(id) where id = $1)`, target).Scan(&h)
		return
	}
	later := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)

	if st, _ := create("web", `{"minutes":20}`); st != 400 {
		t.Errorf("20 minutes accepted: %d", st)
	}
	if st, _ := create("web", `{"minutes":60,"starts_at":"`+time.Now().AddDate(0, 0, 61).UTC().Format(time.RFC3339)+`"}`); st != 400 {
		t.Errorf("61 days ahead accepted: %d", st)
	}
	if st, body := create("web", `{"minutes":60,"starts_at":"`+later+`","reason":"Kernel upgrade","status_pages":true}`); st != 201 {
		t.Fatalf("plan: %d %s", st, body)
	}
	if held("web") {
		t.Error("a planned window holds alerts before it starts")
	}
	var entries int
	s.db.QueryRow(ctx, `select count(*) from status_incidents i join status_incident_updates u on u.incident_id = i.id
		where i.page_id::text = $1 and i.kind = 'maintenance' and i.status = 'scheduled' and u.message = 'Kernel upgrade'`, page).Scan(&entries)
	if entries != 1 {
		t.Errorf("status page entries = %d, want 1", entries)
	}
	if st, _ := create("web", `{"minutes":30,"starts_at":"`+time.Now().Add(150*time.Minute).UTC().Format(time.RFC3339)+`"}`); st != 409 {
		t.Errorf("overlapping window: %d, want 409", st)
	}
	if st, _ := create("db", `{"minutes":60,"starts_at":"`+later+`"}`); st != 201 {
		t.Errorf("same time on another server: %d", st)
	}

	// Starting now holds alerts for the server and its monitors, and its components show under maintenance.
	if st, body := create("web", `{"minutes":30,"reason":"Disk swap"}`); st != 201 {
		t.Fatalf("start now: %d %s", st, body)
	}
	var status string
	s.db.QueryRow(ctx, `select status from status_components where id::text = $1`, comp).Scan(&status)
	if !held(ids["web"]) || !held(mon) || held(ids["db"]) || status != "under_maintenance" {
		t.Errorf("active window: web %v monitor %v db %v component %s", held(ids["web"]), held(mon), held(ids["db"]), status)
	}

	// Ending early releases the hold; cancelling a planned window removes its status page entry.
	var active, planned string
	s.db.QueryRow(ctx, `select id::text from server_maintenance where server_id::text = $1 and status = 'active'`, ids["web"]).Scan(&active)
	s.db.QueryRow(ctx, `select id::text from server_maintenance where server_id::text = $1 and status = 'planned'`, ids["web"]).Scan(&planned)
	end := func(wid string) int {
		r := httptest.NewRequest("POST", "/", nil)
		r.SetPathValue("pid", p)
		r.SetPathValue("sid", ids["web"])
		r.SetPathValue("id", wid)
		w := httptest.NewRecorder()
		s.h(s.endMaintenance)(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid})))
		return w.Code
	}
	if end(active) != 204 || held(ids["web"]) {
		t.Error("ending early didn't release the hold")
	}
	if end(planned) != 204 || end(planned) != 409 {
		t.Error("cancel planned")
	}
	s.db.QueryRow(ctx, `select count(*) from status_incidents where page_id::text = $1 and name = 'Scheduled maintenance'`, page).Scan(&entries)
	if entries != 0 {
		t.Errorf("status page entries after cancel = %d, want 0", entries)
	}

	// A restart window ends by itself once the server has booted after the restart was sent.
	wid := id(t, s, `insert into server_maintenance (project_id, server_id, starts_at, ends_at, action, status, action_at)
		values ($1,$2, now() - interval '5 minutes', now() + interval '1 hour', 'restart', 'active', now() - interval '4 minutes') returning id::text`, p, ids["db"])
	s.runMaintenance(ctx)
	s.db.QueryRow(ctx, `select status from server_maintenance where id::text = $1`, wid).Scan(&status)
	if status != "active" {
		t.Fatalf("ended before the server restarted: %s", status)
	}
	s.db.Exec(ctx, `insert into server_metrics (server_id, ts, cpu, mem_used, mem_total, disk_used, disk_total, net_rx, net_tx, load1, uptime)
		values ($1, now(), 1, 1, 1, 1, 1, 0, 0, 0, 60)`, ids["db"])
	s.runMaintenance(ctx)
	s.db.QueryRow(ctx, `select status from server_maintenance where id::text = $1`, wid).Scan(&status)
	if status != "done" {
		t.Errorf("restart window still %s after the server came back", status)
	}
}

func TestUpdatesEndpoints(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	p, uid, ids := updatesFixture(t, s, map[string]string{"web": "online", "plain": "online"})
	viewer, _ := newTestUser(t, s, "viewer", "x")
	s.db.Exec(ctx, `insert into memberships select org_id, $2, 'viewer' from projects where id::text = $1`, p, viewer)
	call := func(uid string, fn handlerFunc, body string) (int, string) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("pid", p)
		r.SetPathValue("sid", ids["web"])
		w := httptest.NewRecorder()
		s.h(fn)(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid})))
		return w.Code, w.Body.String()
	}
	for _, fn := range []handlerFunc{s.listUpdates, s.serverUpdates, s.listRollouts, s.listMaintenance} {
		if st, body := call(viewer, fn, ""); st != 200 {
			t.Errorf("read endpoint: %d %s", st, body)
		}
	}
	if _, body := call(viewer, s.listUpdates, ""); !strings.Contains(body, `"name":"openssl"`) || !strings.Contains(body, `"security":1`) {
		t.Errorf("listUpdates: %s", body)
	}
	// Viewers can preview a rollout but not start one.
	if st, body := call(viewer, s.createRollout, `{"scope":"packages","packages":[{"name":"tzdata"}],"reboot":"never","dry_run":true}`); st != 200 ||
		!strings.Contains(body, `"plain"`) || strings.Contains(body, `"web"`) {
		t.Errorf("dry run: %d %s", st, body)
	}
	if st, _ := call(viewer, s.createRollout, `{"scope":"all","reboot":"never"}`); st != 403 {
		t.Errorf("viewer started a rollout: %d", st)
	}
	if st, _ := call(uid, s.createRollout, `{"scope":"packages","packages":[{"name":"-o"}],"reboot":"never"}`); st != 400 {
		t.Errorf("bad package name: %d", st)
	}
	if st, body := call(uid, s.createRollout, `{"scope":"all","reboot":"never","starts_at":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`); st != 201 {
		t.Errorf("schedule rollout: %d %s", st, body)
	}
}
