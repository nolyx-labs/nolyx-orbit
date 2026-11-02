package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestRouteArea(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/projects/p/servers":            "servers",
		"/api/v1/projects/p/servers/s/reboot":   "updates",
		"/api/v1/projects/p/servers/s/updates":  "updates",
		"/api/v1/projects/p/servers/s/insight":  "servers",
		"/api/v1/projects/p/apps/a/deployments": "services",
		"/api/v1/projects/p/status-pages/x":     "monitoring",
		"/api/v1/projects/p/secrets":            "secrets",
		"/api/v1/projects/p/overview":           "",
		"/api/v1/projects/p":                    "",
		"/api/v1/orgs/o/members":                "",
	} {
		if got := routeArea(path); got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
	perms := map[string]string{"servers": "viewer", "services": "admin", "secrets": "none"}
	for _, c := range []struct {
		role, area string
		want       int
	}{
		{"member", "servers", 0}, {"member", "services", 1}, {"member", "secrets", -1}, {"member", "databases", -1},
		{"member", "", 1}, {"owner", "secrets", 3},
	} {
		if got := cappedRank(c.role, perms, c.area); got != c.want {
			t.Errorf("%s in %s: got %d, want %d", c.role, c.area, got, c.want)
		}
	}
}

func TestCustomRoles(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	admin, _ := newTestUser(t, s, "admin", "x")
	teamed, _ := newTestUser(t, s, "teamed", "x")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	role := id(t, s, `insert into org_roles (org_id, name, permissions) values ($1,'deployer',
		'{"servers":"viewer","services":"admin","secrets":"none"}') returning id::text`, org)
	team := id(t, s, `insert into teams (org_id, name) values ($1,'ops') returning id::text`, org)
	for _, uid := range []string{admin, teamed} {
		if _, err := s.db.Exec(ctx, `insert into memberships values ($1,$2,'admin',$3)`, org, uid, role); err != nil {
			t.Fatal(err)
		}
	}
	s.db.Exec(ctx, `insert into team_members values ($1,$2)`, team, teamed)
	s.db.Exec(ctx, `insert into team_project_roles values ($1,$2,'admin')`, team, p)

	for _, c := range []struct {
		name, uid, route, min, role string
		status                      int
	}{
		{"capped to viewer", admin, "servers", "member", "", 403},
		{"reads still allowed", admin, "servers/x", "viewer", "viewer", 200},
		{"admin level up to org role", admin, "apps", "admin", "admin", 200},
		{"none blocks the area", admin, "secrets", "viewer", "", 403},
		{"unlisted area is none", admin, "databases", "viewer", "", 403},
		{"project-wide routes uncapped", admin, "overview", "admin", "admin", 200},
		{"team grant raises past the cap", teamed, "secrets", "admin", "admin", 200},
	} {
		r := httptest.NewRequest("GET", "/api/v1/projects/"+p+"/"+c.route, nil)
		r.SetPathValue("pid", p)
		got, err := s.projectAccess(r.WithContext(context.WithValue(ctx, userKey, user{ID: c.uid})), c.min)
		status := 200
		var ae apiError
		if errors.As(err, &ae) {
			status = ae.status
		} else if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if status != c.status || (status == 200 && got.Role != c.role) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, status, got.Role, c.status, c.role)
		}
	}
}
