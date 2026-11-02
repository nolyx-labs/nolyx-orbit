package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestAttention(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	web := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'web','x') returning id::text`, p)
	quiet := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'quiet','x') returning id::text`, p)
	for _, q := range []string{
		`insert into os_update_checks (server_id, packages, reboot_required) values ('` + web + `',
			'[{"name":"openssl","security":true},{"name":"vim","security":false}]', true)`,
		`insert into os_update_checks (server_id, packages) values ('` + quiet + `', '[{"name":"vim","security":false}]')`,
		`insert into server_backups (server_id, service, kind, status, created_at) values ('` + web + `','app/a','scheduled','succeeded', now() - interval '1 day')`,
		`insert into server_backups (server_id, service, kind, status) values ('` + web + `','app/a','scheduled','failed')`,
		`insert into server_backups (server_id, service, kind, status) values ('` + quiet + `','app/a','scheduled','succeeded')`,
		`insert into server_insight (server_id, scan) values ('` + web + `', '{"findings":[{"severity":"high"},{"severity":"low"}]}')`,
		`insert into server_insight (server_id, scan) values ('` + quiet + `', '{"findings":null}')`,
		`insert into monitors (project_id, name, type, target, status) values ('` + p + `','api','tcp','x:1','down')`,
		`insert into monitors (project_id, name, type, target, status, paused) values ('` + p + `','old','tcp','x:1','down', true)`,
	} {
		if _, err := s.db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := s.list(ctx, attentionSQL, p)
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Kind, Name string
		N          int
	}
	json.Unmarshal(raw, &got)
	want := []string{"backup_failed web 1", "findings web 1", "monitor_down api 1", "reboot web 1", "security_updates web 1"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, g := range got {
		if w := want[i]; g.Kind+" "+g.Name+" "+string(rune('0'+g.N)) != w {
			t.Errorf("row %d: got %+v, want %s", i, g, w)
		}
	}
}

func TestFleet(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	uid, _ := newTestUser(t, s, "agency", "x")
	var orgs, projects []string
	for _, name := range []string{"client-a", "client-b", "blocked"} {
		org := id(t, s, `insert into organizations (name, require_mfa) values ($1, $1 = 'blocked') returning id::text`, name)
		s.db.Exec(ctx, `insert into memberships values ($1,$2,'member')`, org, uid)
		orgs = append(orgs, org)
		projects = append(projects, id(t, s, `insert into projects (org_id, name) values ($1,'prod') returning id::text`, org))
	}
	s.db.Exec(ctx, `insert into servers (project_id, name, agent_secret_hash, status) values ($1,'web','x','offline')`, projects[0])
	s.db.Exec(ctx, `insert into monitors (project_id, name, type, target, status) values ($1,'api','tcp','x:1','down')`, projects[0])
	other := id(t, s, `insert into organizations (name) values ('not mine') returning id::text`)
	id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, other)

	get := func(u user) []map[string]any {
		r := httptest.NewRequest("GET", "/api/v1/fleet", nil).WithContext(context.WithValue(ctx, userKey, u))
		w := httptest.NewRecorder()
		if err := s.fleet(w, r); err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	rows := get(user{ID: uid})
	if len(rows) != 2 || rows[0]["org"] != "client-a" || rows[1]["org"] != "client-b" {
		t.Fatalf("rows %v", rows)
	}
	if rows[0]["servers"] != 1.0 || rows[0]["online"] != 0.0 || rows[0]["attention"] != 1.0 {
		t.Errorf("client-a counts %v", rows[0])
	}
	if rows := get(user{ID: uid, Key: &apiKey{OrgID: orgs[1], Role: "viewer"}}); len(rows) != 1 || rows[0]["org"] != "client-b" {
		t.Errorf("key scoping: %v", rows)
	}
}
