package server

import (
	"context"
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditExportAndRetention(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	uid, _ := newTestUser(t, s, "admin", "x")
	keep := id(t, s, `insert into organizations (name) values ('forever') returning id::text`)
	short := id(t, s, `insert into organizations (name, audit_retention_days) values ('short', 90) returning id::text`)
	for _, org := range []string{keep, short} {
		s.db.Exec(ctx, `insert into memberships values ($1,$2,'admin')`, org, uid)
		for _, age := range []string{"1 day", "100 days"} {
			if _, err := s.db.Exec(ctx, `insert into audit_events (org_id, actor, action, target, metadata, ip, created_at)
				values ($1, 'a@x', 'server.delete', 'web, "1"', '{"k":1}', '1.2.3.4', now() - $2::interval)`, org, age); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.retention(ctx)
	count := func(org string) (n int) {
		s.db.QueryRow(ctx, `select count(*) from audit_events where org_id::text = $1 and action = 'server.delete'`, org).Scan(&n)
		return
	}
	if count(keep) != 2 || count(short) != 1 {
		t.Errorf("after retention: forever %d, 90 days %d", count(keep), count(short))
	}

	export := func(format string) string {
		r := httptest.NewRequest("GET", "/api/v1/orgs/"+keep+"/audit/export?format="+format, nil)
		r.SetPathValue("oid", keep)
		w := httptest.NewRecorder()
		if err := s.auditExport(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid}))); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	recs, err := csv.NewReader(strings.NewReader(export("csv"))).ReadAll()
	if err != nil || len(recs) != 3 || recs[0][3] != "action" || recs[1][4] != `web, "1"` || recs[1][7] != `{"k": 1}` {
		t.Errorf("csv %v (%v)", recs, err)
	}
	// The CSV export above is itself audited, so it shows up here.
	if lines := strings.Split(strings.TrimSpace(export("jsonl")), "\n"); len(lines) != 3 || !strings.Contains(lines[0], `"metadata":{"k":1}`) ||
		!strings.Contains(lines[2], `"action":"audit.export"`) {
		t.Errorf("jsonl %v", lines)
	}
}
