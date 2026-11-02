package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"orbit/internal/protocol"
)

func TestMonitorLocations(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	other := id(t, s, `insert into projects (org_id, name) values ($1,'other') returning id::text`, org)
	eu := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'eu','x') returning id::text`, p)
	us := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'us','x') returning id::text`, p)
	stranger := id(t, s, `insert into servers (project_id, name, agent_secret_hash) values ($1,'x','x') returning id::text`, other)
	m := id(t, s, `insert into monitors (project_id, name, type, target, locations) values ($1,'api','tcp','x:1', array[$2,$3]::uuid[]) returning id::text`, p, eu, us)

	up, down := protocol.MonitorResult{OK: true}, protocol.MonitorResult{Error: "timeout"}
	// No location results yet: the control plane alone decides.
	if r := s.vote(ctx, m, down); r.OK {
		t.Fatal("control plane alone failing should fail")
	}
	report := func(ac *agentConn, ok bool) {
		if err := s.storeLocationResults(ctx, ac, protocol.MonitorResults{Results: []protocol.MonitorResult{
			{MonitorID: m, TS: time.Now(), OK: ok, Error: map[bool]string{false: "refused"}[ok]}}}); err != nil {
			t.Fatal(err)
		}
	}
	report(&agentConn{serverID: eu, projectID: p}, true)
	report(&agentConn{serverID: us, projectID: p}, true)
	report(&agentConn{serverID: stranger, projectID: other}, false) // not a location of this monitor: ignored
	if r := s.vote(ctx, m, down); !r.OK {
		t.Errorf("1 of 3 failing should be up, got %+v", r)
	}
	report(&agentConn{serverID: us, projectID: p}, false)
	if r := s.vote(ctx, m, down); r.OK || !strings.Contains(r.Error, "2 of 3") {
		t.Errorf("2 of 3 failing should be down, got %+v", r)
	}
	if r := s.vote(ctx, m, up); !r.OK {
		t.Errorf("1 of 3 failing should be up, got %+v", r)
	}
	var n int
	s.db.QueryRow(ctx, `select count(*) from monitor_checks where monitor_id::text = $1`, m).Scan(&n)
	if n != 3 {
		t.Errorf("want 3 stored location results, got %d", n)
	}
}
