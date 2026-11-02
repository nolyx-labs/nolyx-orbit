package agent

import "testing"

func TestRedact(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// masked
		{`connecting with password=hunter2 to db`, `connecting with password=[redacted] to db`},
		{`DB_PASSWORD: s3cr3t!`, `DB_PASSWORD: [redacted]`},
		{`{"user":"bob","password":"p@ss \"x\"","ok":true}`, `{"user":"bob","password":"[redacted]","ok":true}`},
		{`config apiKey='abc123' loaded`, `config apiKey='[redacted]' loaded`},
		{`GET /cb?access_token=abc.def&state=1`, `GET /cb?access_token=[redacted]&state=1`},
		{`aws_secret_access_key = wJalrXUtnFEMI/K7MDENG`, `aws_secret_access_key = [redacted]`},
		{`Authorization: Bearer eyJhbGciOi.payload.sig`, `Authorization: Bearer [redacted]`},
		{`"authorization": "Basic dXNlcjpwYXNz"`, `"authorization": "Basic [redacted]"`},
		{`curl -H 'Bearer abcdefghijklmnopqrstuvwxyz0123'`, `curl -H 'Bearer [redacted]'`},
		{`key AKIAIOSFODNN7EXAMPLE in use`, `key [redacted] in use`},
		{`-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY----- done`, `[redacted] done`},
		{`jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`, `jwt [redacted]`},
		{`using ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789`, `using [redacted]`},
		{`pat github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz`, `pat [redacted]`},
		{`slack xoxb-123456789012-abcdefABCDEF`, `slack [redacted]`},
		{`stripe sk_live_51HabcdefGHIJKLMNOPqrstu`, `stripe [redacted]`},
		{`DATABASE_URL=postgres://orbit:supersecret@db:5432/orbit`, `DATABASE_URL=postgres://orbit:[redacted]@db:5432/orbit`},
		{`enrolling with orb_enr_AbCdEfGhIjKlMnOpQrStUvWxYz012345`, `enrolling with [redacted]`},
		{`GITHUB_TOKEN=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789`, `GITHUB_TOKEN=[redacted]`},
		{`X-Auth-Token: Bearer abcdefghijklmnopqrstuvwxyz0123`, `X-Auth-Token: [redacted] [redacted]`},
		// unchanged
		{`GET /api/v1/users 200 12ms`, ``},
		{`invalid password for user bob`, ``},
		{`token expired, refreshing`, ``},
		{`Authorization header missing`, ``},
		{`bearer token required`, ``},
		{`basic auth disabled`, ``},
		{`listening on http://0.0.0.0:8080/health`, ``},
		{`see https://docs.example.com/a@b`, ``},
		{`2024-05-01 INFO worker started pid=42 queue=default`, ``},
		{`user@example.com logged in`, ``},
		{`Skipping key rotation; AKIA prefix only`, ``},
	} {
		want := c.want
		if want == "" {
			want = c.in
		}
		var r redactor
		if got := r.line(c.in); got != want {
			t.Errorf("\n in: %s\ngot: %s\nwant: %s", c.in, got, want)
		}
	}
}

func TestRedactKeyBlock(t *testing.T) {
	var r redactor
	lines := []string{"loading key", "-----BEGIN PRIVATE KEY-----", "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", "-----END PRIVATE KEY-----", "ready"}
	want := []string{"loading key", "[redacted]", "[redacted]", "[redacted]", "ready"}
	for i, l := range lines {
		if got := r.line(l); got != want[i] {
			t.Errorf("line %d: %q, want %q", i, got, want[i])
		}
	}
}

func BenchmarkRedact(b *testing.B) {
	var r redactor
	for b.Loop() {
		r.line(`2024-05-01T10:00:00Z INFO request completed method=GET path=/api/v1/projects status=200 duration=12ms`)
	}
}
