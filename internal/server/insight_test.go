package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"orbit/internal/protocol"
	"strings"
	"testing"
)

func findingKeys(fs []finding) []string {
	var keys []string
	for _, f := range fs {
		keys = append(keys, f.Severity+":"+f.Key)
	}
	return keys
}

func TestSecurityFindings(t *testing.T) {
	ports := []protocol.ListenPort{
		{Proto: "tcp", Address: "0.0.0.0", Port: 22, Scope: "all", Process: "sshd"},
		{Proto: "tcp", Address: "::", Port: 22, Scope: "all", Process: "sshd"},
		{Proto: "tcp", Address: "0.0.0.0", Port: 443, Scope: "all", Process: "caddy"},
		{Proto: "tcp", Address: "::", Port: 443, Scope: "all", Process: "caddy"},
		{Proto: "tcp", Address: "127.0.0.1", Port: 6379, Scope: "localhost", Process: "redis-server"},
		{Proto: "tcp", Address: "0.0.0.0", Port: 5432, Scope: "all", Process: "docker-proxy", Container: "db"},
		{Proto: "tcp", Address: "0.0.0.0", Port: 9100, Scope: "all", Process: "node_exporter"},
	}
	ufw := protocol.Firewall{Tool: "ufw", Active: true, Incoming: "deny", Outgoing: "allow", Rules: []protocol.FirewallRule{
		{Port: "22", Proto: "tcp", Action: "ALLOW IN", From: "Anywhere"},
		{Port: "80,443", Proto: "tcp", Action: "ALLOW IN", From: "Anywhere"},
		{Port: "9100", Proto: "tcp", Action: "ALLOW IN", From: "10.0.0.0/8"},
	}}
	got := strings.Join(findingKeys(securityFindings(protocol.SecurityFacts{
		Ports: ports, Firewall: ufw, AutoUpdates: "unattended-upgrades", SecurityUpdates: 0,
		SSH: protocol.SSHConfig{Installed: true, PermitRootLogin: "prohibit-password", PasswordAuth: "no", Ports: []int{22}},
	})), " ")
	// 5432 is published by Docker, which bypasses ufw; 9100 is only allowed from a private range.
	if got != "high:port.tcp.5432 low:port.tcp.443" {
		t.Errorf("hardened server: %s", got)
	}

	got = strings.Join(findingKeys(securityFindings(protocol.SecurityFacts{
		Ports: ports, Firewall: protocol.Firewall{Tool: "ufw"}, AutoUpdates: "", SecurityUpdates: 3, RebootRequired: true,
		SSH: protocol.SSHConfig{Installed: true, PermitRootLogin: "yes", PasswordAuth: "yes"},
	})), " ")
	want := "high:port.tcp.5432 high:ssh.root_login medium:firewall.inactive medium:port.tcp.9100 medium:ssh.password_auth medium:updates.automatic medium:updates.pending low:port.tcp.443 low:reboot.required"
	if got != want {
		t.Errorf("open server:\n got %s\nwant %s", got, want)
	}

	got = strings.Join(findingKeys(securityFindings(protocol.SecurityFacts{Container: true, Ports: ports[6:], AutoUpdates: "unknown", SecurityUpdates: -1})), " ")
	if got != "medium:port.tcp.9100" {
		t.Errorf("container mode: %s", got)
	}
}

func TestPortMatches(t *testing.T) {
	for spec, ports := range map[string][2][]int{
		"22":         {{22}, {23}},
		"80,443":     {{80, 443}, {8080}},
		"8000:8100":  {{8000, 8050, 8100}, {7999, 8101}},
		"OpenSSH":    {{22}, {80}},
		"Anywhere":   {{1, 65535}, {}},
		"Nginx Full": {{80, 443}, {22}},
		"cockpit":    {{}, {9090}},
	} {
		for _, p := range ports[0] {
			if !portMatches(spec, p) {
				t.Errorf("%q should match %d", spec, p)
			}
		}
		for _, p := range ports[1] {
			if portMatches(spec, p) {
				t.Errorf("%q shouldn't match %d", spec, p)
			}
		}
	}
}

func TestServerGroupsEnvironmentsAndScans(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	admin, _ := newTestUser(t, s, "admin", "x")
	viewer, _ := newTestUser(t, s, "viewer", "x")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	other := id(t, s, `insert into projects (org_id, name) values ($1,'other') returning id::text`, org)
	s.db.Exec(ctx, `insert into memberships values ($1,$2,'admin'), ($1,$3,'viewer')`, org, admin, viewer)
	srv := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'web-1','x') returning id::text`, p)
	foreign := id(t, s, `insert into server_groups (project_id, name) values ($1,'theirs') returning id::text`, other)

	call := func(uid, method string, fn handlerFunc, body string, path ...string) (int, string) {
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("pid", p)
		r.SetPathValue("sid", srv)
		if len(path) > 0 {
			r.SetPathValue("gid", path[0])
		}
		w := httptest.NewRecorder()
		s.h(fn)(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid})))
		return w.Code, w.Body.String()
	}

	if st, _ := call(viewer, "PATCH", s.updateServer, `{"environment":"staging"}`); st != 403 {
		t.Fatalf("viewer changed environment: %d", st)
	}
	if st, _ := call(admin, "PATCH", s.updateServer, `{"environment":"qa"}`); st != 400 {
		t.Fatalf("unknown environment accepted: %d", st)
	}
	if st, _ := call(admin, "PATCH", s.updateServer, `{"environment":"staging"}`); st != 200 {
		t.Fatalf("set environment: %d", st)
	}
	st, body := call(admin, "POST", s.createServerGroup, `{"name":" web "}`)
	if st != 201 {
		t.Fatalf("create group: %d %s", st, body)
	}
	var g struct{ ID string }
	json.Unmarshal([]byte(body), &g)
	if st, _ := call(admin, "POST", s.createServerGroup, `{"name":"web"}`); st != 409 {
		t.Fatalf("duplicate group: %d", st)
	}
	if st, _ := call(viewer, "POST", s.createServerGroup, `{"name":"db"}`); st != 403 {
		t.Fatalf("viewer created group: %d", st)
	}
	if st, body := call(admin, "PUT", s.putServerGroups, `{"group_ids":["`+g.ID+`","`+foreign+`"]}`); st != 200 {
		t.Fatalf("set groups: %d %s", st, body)
	}

	// The first scan raises events for high findings; an identical rescan doesn't repeat them.
	facts := protocol.SecurityFacts{
		AutoUpdates: "unattended-upgrades", SecurityUpdates: -1,
		SSH:   protocol.SSHConfig{Installed: true, PermitRootLogin: "yes"},
		Ports: []protocol.ListenPort{{Proto: "tcp", Address: "0.0.0.0", Port: 5432, Scope: "all", Process: "postgres"}},
	}
	for range 2 {
		if err := s.storeScan(ctx, p, srv, facts); err != nil {
			t.Fatal(err)
		}
	}
	var scanEvents int
	s.db.QueryRow(ctx, `select count(*) from events where server_id = $1 and message like 'Security scan%'`, srv).Scan(&scanEvents)
	if scanEvents != 2 {
		t.Errorf("scan events = %d, want 2 (root login, PostgreSQL port)", scanEvents)
	}

	st, body = call(viewer, "GET", s.listServers, "")
	var list []struct {
		Environment string
		Groups      []struct{ Name string }
		Security    struct{ High, Medium, Low int }
	}
	if err := json.Unmarshal([]byte(body), &list); st != 200 || err != nil || len(list) != 1 {
		t.Fatalf("list servers: %d %s", st, body)
	}
	if l := list[0]; l.Environment != "staging" || len(l.Groups) != 1 || l.Groups[0].Name != "web" || l.Security.High != 2 || l.Security.Medium != 1 {
		t.Errorf("server row: %+v", l)
	}

	// Inventory: a service that starts failing raises one event, and only reported units can be acted on.
	inv := protocol.Inventory{Services: []protocol.SystemdService{{Unit: "nginx.service", Active: "active"}}}
	s.storeInventory(ctx, p, srv, inv)
	inv.Services[0].Active = "failed"
	s.storeInventory(ctx, p, srv, inv)
	s.storeInventory(ctx, p, srv, inv)
	var failEvents int
	s.db.QueryRow(ctx, `select count(*) from events where server_id = $1 and message like 'Service nginx.service failed%'`, srv).Scan(&failEvents)
	if failEvents != 1 {
		t.Errorf("failure events = %d", failEvents)
	}
	if st, _ := call(admin, "POST", s.serviceAction, `{"unit":"nginx.service","action":"rm"}`); st != 400 {
		t.Errorf("bad action: %d", st)
	}
	if st, _ := call(admin, "POST", s.serviceAction, `{"unit":"cron.service","action":"restart"}`); st != 404 {
		t.Errorf("unreported unit: %d", st)
	}
	if st, _ := call(admin, "POST", s.serviceAction, `{"unit":"nginx.service","action":"restart"}`); st != 409 {
		t.Errorf("known unit on an offline server should reach sendCommand: %d", st)
	}
	if st, _ := call(viewer, "POST", s.serviceAction, `{"unit":"nginx.service","action":"restart"}`); st != 403 {
		t.Errorf("viewer acted on a service: %d", st)
	}
	if st, _ := call(admin, "POST", s.firewallAction, `{"action":"open","port":8080,"proto":"tcp","source":"nope"}`); st != 400 {
		t.Errorf("bad firewall source: %d", st)
	}

	if st, _ := call(admin, "POST", s.clearServerData, `{"confirm":"web"}`); st != 400 {
		t.Errorf("clear with wrong confirmation: %d", st)
	}
	if st, _ := call(admin, "POST", s.clearServerData, `{"confirm":"web-1"}`); st != 204 {
		t.Errorf("clear: %d", st)
	}
	var left int
	s.db.QueryRow(ctx, `select count(*) from events where server_id = $1`, srv).Scan(&left)
	if left != 0 {
		t.Errorf("%d events left after clearing", left)
	}
	if st, _ := call(admin, "DELETE", s.deleteServerGroup, "", g.ID); st != 204 {
		t.Errorf("delete group: %d", st)
	}
	var members int
	s.db.QueryRow(ctx, `select count(*) from server_group_members where server_id = $1`, srv).Scan(&members)
	if members != 0 {
		t.Errorf("membership survived group deletion")
	}
}
