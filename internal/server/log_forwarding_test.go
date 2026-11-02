package server

import (
	"context"
	"os"
	"testing"
)

func TestForwardLogs(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'shop') returning id::text`, org)
	other := id(t, s, `insert into projects (org_id, name) values ($1,'other') returning id::text`, org)
	web := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'web','x') returning id::text`, p)
	ow := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'o','x') returning id::text`, other)
	logLine := func(project, server, line string) {
		if _, err := s.db.Exec(ctx, `insert into log_lines (project_id, server_id, container_id, container, ts, stream, level, line, unit)
			values ($1, $2, '', '', now(), 'journal', 'info', $3, 'nginx.service')`, project, server, line); err != nil {
			t.Fatal(err)
		}
	}
	logLine(p, web, "one")
	logLine(other, ow, "not ours")
	logLine(p, web, "two")

	// The test database stands in for the customer's; it's on localhost, which destinations normally can't reach.
	if _, err := s.destConn(ctx, "mysql://x"); err == nil {
		t.Error("non-PostgreSQL URL accepted")
	}
	if _, err := s.destConn(ctx, os.Getenv("TEST_DATABASE_URL")); err == nil {
		t.Error("private destination allowed")
	}
	s.cfg.AllowPrivateWebhooks = true
	s.db.Exec(ctx, `drop table if exists orbit_logs`)
	if _, err := s.db.Exec(ctx, `insert into log_destinations (project_id, url_encrypted) values ($1, $2)`, p, s.encrypt([]byte(os.Getenv("TEST_DATABASE_URL")))); err != nil {
		t.Fatal(err)
	}
	s.forwardLogs(ctx) // first tick only records which ids have committed
	s.forwardLogs(ctx)
	var n int
	var lastErr string
	s.db.QueryRow(ctx, `select count(*) from orbit_logs where project = 'shop' and unit = 'nginx.service' and server = 'web'`).Scan(&n)
	s.db.QueryRow(ctx, `select last_error from log_destinations where project_id::text = $1`, p).Scan(&lastErr)
	if n != 2 || lastErr != "" {
		t.Fatalf("want 2 forwarded lines, got %d (error %q)", n, lastErr)
	}
	logLine(p, web, "three")
	s.forwardLogs(ctx)
	s.db.Exec(ctx, `update log_destinations set forwarded_id = 0 where project_id::text = $1`, p) // a retry copies nothing twice
	s.forwardLogs(ctx)
	s.db.QueryRow(ctx, `select count(*) from orbit_logs`).Scan(&n)
	if n != 3 {
		t.Errorf("want 3 lines after the next tick and a retry, got %d", n)
	}
}
