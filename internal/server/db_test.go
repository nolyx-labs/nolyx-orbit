package server

// Tests against a real Postgres: set TEST_DATABASE_URL to a throwaway database (CI does). Skipped otherwise.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func testServer(t *testing.T) *Server {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := New(context.Background(), Config{DatabaseURL: url, AppURL: "http://orbit.test", EncryptionKey: strings.Repeat("ab", 32)},
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// id runs an insert ... returning id::text.
func id(t *testing.T, s *Server, sql string, args ...any) string {
	t.Helper()
	var out string
	if err := s.db.QueryRow(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func newTestUser(t *testing.T, s *Server, name, password string) (string, string) {
	email := name + "-" + newUUID() + "@orbit.test"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	return id(t, s, `insert into users (email, name, password_hash) values ($1,$1,$2) returning id::text`, email, string(hash)), email
}

func TestProjectAccess(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	owner, _ := newTestUser(t, s, "owner", "x")
	viewer, _ := newTestUser(t, s, "viewer", "x")
	outsider, _ := newTestUser(t, s, "outsider", "x")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p1 := id(t, s, `insert into projects (org_id, name) values ($1,'p1') returning id::text`, org)
	p2 := id(t, s, `insert into projects (org_id, name) values ($1,'p2') returning id::text`, org)
	team := id(t, s, `insert into teams (org_id, name) values ($1,'ops') returning id::text`, org)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into memberships values ($1,$2,'owner')`, []any{org, owner}},
		{`insert into memberships values ($1,$2,'viewer')`, []any{org, viewer}},
		{`insert into team_members values ($1,$2)`, []any{team, viewer}},
		{`insert into team_project_roles values ($1,$2,'admin')`, []any{team, p1}},
	} {
		if _, err := s.db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		name     string
		u        user
		pid, min string
		role     string
		status   int
	}{
		{"owner", user{ID: owner}, p1, "owner", "owner", 200},
		{"team grant raises viewer", user{ID: viewer}, p1, "admin", "admin", 200},
		{"no grant on other project", user{ID: viewer}, p2, "member", "", 403},
		{"org role still applies", user{ID: viewer}, p2, "viewer", "viewer", 200},
		{"not a member", user{ID: outsider}, p1, "viewer", "", 404},
		{"bad project id", user{ID: owner}, "nope", "viewer", "", 404},
		{"key capped at its role", user{ID: owner, Key: &apiKey{OrgID: org, Role: "viewer"}}, p1, "member", "", 403},
		{"key scoped to another project", user{ID: owner, Key: &apiKey{OrgID: org, ProjectID: p2, Role: "admin"}}, p1, "viewer", "", 404},
		{"key never exceeds its creator", user{ID: viewer, Key: &apiKey{OrgID: org, Role: "admin"}}, p2, "member", "", 403},
		{"key in its project", user{ID: owner, Key: &apiKey{OrgID: org, ProjectID: p1, Role: "admin"}}, p1, "admin", "admin", 200},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("pid", c.pid)
		p, err := s.projectAccess(r.WithContext(context.WithValue(ctx, userKey, c.u)), c.min)
		status := 200
		var ae apiError
		if errors.As(err, &ae) {
			status = ae.status
		} else if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if status != c.status || (status == 200 && p.Role != c.role) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, status, p.Role, c.status, c.role)
		}
	}
}

func post(t *testing.T, s *Server, path string, body map[string]string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// enableMFA turns on 2FA with the RFC 6238 test secret and returns the recovery codes.
func enableMFA(t *testing.T, s *Server, uid string) ([]byte, []string) {
	secret := []byte("12345678901234567890")
	var codes []string
	err := pgx.BeginFunc(context.Background(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `update users set totp_secret_encrypted = $2, totp_enabled_at = now() where id = $1`,
			uid, s.encrypt(secret)); err != nil {
			return err
		}
		var err error
		codes, err = newRecoveryCodes(context.Background(), tx, uid)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

func TestLoginWithMFA(t *testing.T) {
	s := testServer(t)
	uid, email := newTestUser(t, s, "mfa", "correct horse battery")
	secret, codes := enableMFA(t, s, uid)
	login := func(password, code string) (int, map[string]any) {
		return post(t, s, "/api/v1/auth/login", map[string]string{"email": email, "password": password, "code": code})
	}

	if st, out := login("correct horse battery", ""); st != 401 || out["mfa_required"] != true {
		t.Fatalf("password only: %d %v", st, out)
	}
	if st, _ := login("correct horse battery", "000000"); st != 401 {
		t.Fatalf("wrong code accepted: %d", st)
	}
	code := totp(secret, time.Now().Unix()/30)
	if st, out := login("correct horse battery", code); st != 200 {
		t.Fatalf("valid code: %d %v", st, out)
	}
	if st, _ := login("correct horse battery", code); st != 401 {
		t.Fatalf("replayed code accepted: %d", st)
	}
	if st, _ := login("correct horse battery", strings.ToUpper(codes[0])); st != 200 {
		t.Fatalf("recovery code rejected: %d", st)
	}
	if st, _ := login("correct horse battery", codes[0]); st != 401 {
		t.Fatalf("recovery code reused: %d", st)
	}
	if st, _ := login("wrong", code); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	for range 10 {
		login("wrong", "")
	}
	if st, _ := login("correct horse battery", codes[1]); st != 429 {
		t.Fatalf("no lockout after repeated failures: %d", st)
	}
}

func TestPasswordResetKeepsMFA(t *testing.T) {
	s := testServer(t)
	uid, _ := newTestUser(t, s, "reset", "old password 123")
	_, codes := enableMFA(t, s, uid)
	link, err := s.newPasswordReset(context.Background(), uid, false)
	if err != nil {
		t.Fatal(err)
	}
	token := link[strings.LastIndex(link, "/")+1:]
	confirm := func(code string) int {
		st, _ := post(t, s, "/api/v1/auth/password-reset/confirm", map[string]string{"token": token, "password": "new password 123", "code": code})
		return st
	}
	if st := confirm(""); st != 401 {
		t.Fatalf("emailed link bypassed 2FA: %d", st)
	}
	if st := confirm(codes[0]); st != 200 {
		t.Fatalf("reset with recovery code: %d", st)
	}
	if st := confirm(codes[1]); st != 404 {
		t.Fatalf("reset link reused: %d", st)
	}

	// An operator's recovery link (orbit reset-password) turns 2FA off instead.
	link, _ = s.newPasswordReset(context.Background(), uid, true)
	token = link[strings.LastIndex(link, "/")+1:]
	if st := confirm(""); st != 200 {
		t.Fatalf("recovery link: %d", st)
	}
	var on bool
	s.db.QueryRow(context.Background(), `select totp_enabled_at is not null from users where id = $1`, uid).Scan(&on)
	if on {
		t.Fatal("recovery link left 2FA on")
	}
}

func TestSignupClosedExceptInvites(t *testing.T) {
	s := testServer(t)
	newTestUser(t, s, "existing", "x") // so this isn't the first-user case
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	email := "invitee-" + newUUID() + "@orbit.test"
	token, hash := newToken("orb_inv_")
	if _, err := s.db.Exec(context.Background(), `insert into invites (org_id, email, role, token_hash, expires_at) values ($1,$2,'member',$3,now() + interval '1 day')`,
		org, email, hash); err != nil {
		t.Fatal(err)
	}
	signup := func(email, invite string) int {
		st, _ := post(t, s, "/api/v1/auth/signup", map[string]string{"email": email, "password": "long enough pw", "invite": invite})
		return st
	}
	if st := signup("stranger-"+newUUID()+"@orbit.test", ""); st != 403 {
		t.Fatalf("closed sign-up allowed a stranger: %d", st)
	}
	if st := signup("other-"+newUUID()+"@orbit.test", token); st != 403 {
		t.Fatalf("invite redeemed for a different email: %d", st)
	}
	if st := signup(email, token); st != 200 {
		t.Fatalf("invitee couldn't sign up: %d", st)
	}
}

func TestRequireMFA(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	admin, _ := newTestUser(t, s, "admin", "x")
	inTeam, _ := newTestUser(t, s, "in-team", "x")
	other, _ := newTestUser(t, s, "other", "x")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	team := id(t, s, `insert into teams (org_id, name) values ($1,'ops') returning id::text`, org)
	for _, uid := range []string{admin, inTeam, other} {
		s.db.Exec(ctx, `insert into memberships values ($1,$2,'admin')`, org, uid)
	}
	s.db.Exec(ctx, `insert into team_members values ($1,$2)`, team, inTeam)

	call := func(uid, method, oid, tid string, fn handlerFunc, body string) int {
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("pid", p)
		r.SetPathValue("oid", oid)
		r.SetPathValue("tid", tid)
		w := httptest.NewRecorder()
		s.h(fn)(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid})))
		return w.Code
	}
	overview := func(uid string) int {
		return call(uid, "GET", org, "", func(w http.ResponseWriter, r *http.Request) error {
			_, err := s.projectAccess(r, "viewer")
			return err
		}, "")
	}

	// Requiring 2FA needs it on your own account first, so an admin can't lock themselves out.
	if st := call(admin, "PATCH", org, "", s.updateOrg, `{"require_mfa":true}`); st != 400 {
		t.Fatalf("admin without 2FA required it: %d", st)
	}
	enableMFA(t, s, admin)
	if st := call(admin, "PUT", org, team, s.putTeam, `{"require_mfa":true,"user_ids":["`+inTeam+`"],"grants":[]}`); st != 204 {
		t.Fatalf("team require_mfa: %d", st)
	}
	if overview(inTeam) != 403 || overview(other) != 200 {
		t.Fatal("team requirement should block only its members")
	}
	if st := call(admin, "PATCH", org, "", s.updateOrg, `{"require_mfa":true}`); st != 204 {
		t.Fatalf("org require_mfa: %d", st)
	}
	if overview(other) != 403 || overview(admin) != 200 {
		t.Fatal("org requirement should block members without 2FA only")
	}
	if call(other, "GET", org, "", s.getOrg, "") != 403 {
		t.Fatal("orgAccess ignored the requirement")
	}
	enableMFA(t, s, other)
	if overview(other) != 200 {
		t.Fatal("still blocked after turning 2FA on")
	}
	if st := call(other, "POST", org, "", s.mfaDisable, `{"password":"x","code":"000000"}`); st != 409 {
		t.Fatalf("2FA turned off while the org requires it: %d", st)
	}
}

func TestMonitorUptime(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	mon := func(name string) string {
		return id(t, s, `insert into monitors (project_id, name, type, target, created_at) values ($1,$2,'tcp','x:1', now() - interval '10 hours') returning id::text`, p, name)
	}
	healthy, flaky := mon("healthy"), mon("flaky")
	// One hour down in the last ten, plus an older outage outside the window that mustn't count.
	s.db.Exec(ctx, `insert into monitor_outages (monitor_id, started_at, ended_at) values
		($1, now() - interval '3 hours', now() - interval '2 hours'), ($1, now() - interval '20 hours', now() - interval '19 hours')`, flaky)
	for m, want := range map[string]float64{healthy: 100, flaky: 80} { // over the last 5 hours, flaky was down for 1
		var got float64
		if err := s.db.QueryRow(ctx, `select monitor_uptime($1, now() - interval '5 hours')`, m).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got < want-0.01 || got > want+0.01 {
			t.Errorf("uptime = %v, want %v", got, want)
		}
	}
}

func TestStatusComponents(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	mon := id(t, s, `insert into monitors (project_id, name, type, target, status) values ($1,'api','tcp','x:1','down') returning id::text`, p)
	srv := id(t, s, `insert into servers (project_id, name, agent_secret_hash, status) values ($1,'web-1','x','offline') returning id::text`, p)
	page := id(t, s, `insert into status_pages (project_id, name, slug) values ($1,'Acme','acme-`+newUUID()[:8]+`') returning id::text`, p)
	comp := func(name, src string, sid any) string {
		return id(t, s, `insert into status_components (page_id, name, source_type, source_id) values ($1,$2,$3,$4) returning id::text`, page, name, src, sid)
	}
	cMon, cSrv, cManual := comp("API", "monitor", mon), comp("Web", "server", srv), comp("Email", "manual", nil)
	status := func(c string) (st string) {
		s.db.QueryRow(ctx, `select status from status_components where id::text = $1`, c).Scan(&st)
		return
	}
	expect := func(step string, want map[string]string) {
		t.Helper()
		s.updateStatusPages(ctx)
		for c, w := range want {
			if got := status(c); got != w {
				t.Errorf("%s: component status = %s, want %s", step, got, w)
			}
		}
	}
	incident := func(kind, st string, comps map[string]string) string { // maintenance starts a minute ago, ends in an hour
		iid := id(t, s, `insert into status_incidents (page_id, kind, name, impact, status, scheduled_for, scheduled_until)
			values ($1, $2, 'x', case when $2 = 'maintenance' then 'maintenance' else 'major' end, $3,
			  case when $2 = 'maintenance' then now() - interval '1 minute' end, case when $2 = 'maintenance' then now() + interval '1 hour' end)
			returning id::text`, page, kind, st)
		for c, cs := range comps {
			s.db.Exec(ctx, `insert into status_incident_components values ($1,$2,$3)`, iid, c, cs)
		}
		return iid
	}

	expect("sources", map[string]string{cMon: "major_outage", cSrv: "major_outage", cManual: "operational"})

	inc := incident("incident", "investigating", map[string]string{cManual: "partial_outage", cMon: "degraded_performance"})
	expect("open incident", map[string]string{cManual: "partial_outage", cMon: "major_outage"}) // never hides a worse source status
	s.db.Exec(ctx, `update status_incidents set status = 'resolved' where id::text = $1`, inc)
	expect("resolved", map[string]string{cManual: "operational"})

	// Scheduled maintenance whose start has passed begins on its own and shows instead of the server's outage.
	m := incident("maintenance", "scheduled", map[string]string{cSrv: "under_maintenance"})
	expect("maintenance", map[string]string{cSrv: "under_maintenance", cMon: "major_outage"})
	var mst string
	s.db.QueryRow(ctx, `select status from status_incidents where id::text = $1`, m).Scan(&mst)
	if mst != "in_progress" {
		t.Errorf("maintenance status = %s, want in_progress", mst)
	}
	var held bool
	s.db.QueryRow(ctx, `select exists(`+maintenanceSQL+`)`).Scan(&held)
	if !held {
		t.Error("alerts aren't held for a server under maintenance")
	}

	var rows int
	s.db.QueryRow(ctx, `select count(*) from status_component_history where component_id::text = $1`, cManual).Scan(&rows)
	if rows != 3 { // operational → partial_outage → operational
		t.Errorf("history rows = %d, want 3", rows)
	}
}
