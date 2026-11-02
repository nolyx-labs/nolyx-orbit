package server

import (
	"context"
	"testing"
)

func TestGuardHostMounts(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	binds := id(t, s, `insert into applications (project_id, name, image, carried) values ($1, 'shop', 'shop:1',
		'{"mounts":[{"type":"volume","source":"d","target":"/d"},{"type":"bind","source":"/srv","target":"/srv"}]}') returning id::text`, p)
	vols := id(t, s, `insert into applications (project_id, name, image, carried) values ($1, 'db', 'pg:17',
		'{"mounts":[{"type":"volume","source":"d","target":"/d"}]}') returning id::text`, p)
	if _, err := s.db.Exec(ctx, `insert into deployments (application_id, number, image) values ($1, 1, 'shop:1')`, binds); err != nil {
		t.Fatal(err)
	}
	member, admin := project{ID: p, Role: "member"}, project{ID: p, Role: "admin"}
	for _, c := range []struct {
		p           project
		app, image  string
		wantAllowed bool
	}{
		{member, binds, "", false},       // settings
		{member, binds, "evil:1", false}, // an image it never ran
		{member, binds, "shop:1", true},  // a rollback to what it ran before
		{admin, binds, "evil:1", true},
		{member, vols, "", true}, // named volumes don't reach the host's files
	} {
		if err := s.guardHostMounts(ctx, c.p, c.app, c.image); (err == nil) != c.wantAllowed {
			t.Errorf("%s on %s with image %q: err %v", c.p.Role, c.app, c.image, err)
		}
	}
}
