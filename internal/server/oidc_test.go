package server

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is an OpenID provider: discovery, JWKS (one RSA and one EC key) and a token endpoint that checks PKCE.
type fakeIdP struct {
	*httptest.Server
	t     *testing.T
	rsa   *rsa.PrivateKey
	ec    *ecdsa.PrivateKey
	rsaID string // the RSA key's kid; changing it simulates a key rotation

	mu        sync.Mutex
	jwksHits  int
	codes     map[string][2]string // code -> challenge, nonce
	lastForm  url.Values
	lastBasic string
	sub       string
	email     string
	extra     map[string]any // overrides/additions to the issued claims; nil values delete
	signEC    bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f := &fakeIdP{t: t, rsa: rk, ec: ek, rsaID: "rsa-1", codes: map[string][2]string{}, sub: "sub-1", email: "user@orbit.test"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize?prompt=login",
			"token_endpoint": f.URL + "/token", "jwks_uri": f.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.jwksHits++
		b := base64.RawURLEncoding.EncodeToString
		x, y := f.ec.X.FillBytes(make([]byte, 32)), f.ec.Y.FillBytes(make([]byte, 32))
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": f.rsaID, "use": "sig", "n": b(f.rsa.N.Bytes()), "e": b(big.NewInt(int64(f.rsa.E)).Bytes())},
			{"kty": "EC", "kid": "ec-1", "crv": "P-256", "x": b(x), "y": b(y)},
			{"kty": "RSA", "kid": "enc-1", "use": "enc", "n": b(f.rsa.N.Bytes()), "e": "AQAB"},
		}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.lastForm, f.lastBasic = r.PostForm, r.Header.Get("Authorization")
		c, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c[0] {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.token(f.claims(c[1])), "token_type": "Bearer"})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIdP) claims(nonce string) map[string]any {
	now := time.Now().Unix()
	c := map[string]any{"iss": f.URL, "sub": f.sub, "aud": "orbit", "exp": now + 300, "iat": now, "nonce": nonce,
		"email": f.email, "email_verified": true, "name": "Test User"}
	for k, v := range f.extra {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func (f *fakeIdP) token(claims map[string]any) string {
	if f.signEC {
		return signJWT(f.t, f.ec, "ES256", "ec-1", claims)
	}
	return signJWT(f.t, f.rsa, "RS256", f.rsaID, claims)
}

func signJWT(t *testing.T, key crypto.Signer, alg, kid string, claims map[string]any) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	in := enc(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"}) + "." + enc(claims)
	h := sha256.Sum256([]byte(in))
	var sig []byte
	switch k := key.(type) {
	case *rsa.PrivateKey:
		sig, _ = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, k, h[:])
		if err != nil {
			t.Fatal(err)
		}
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// authorize plays the provider's login page: it remembers the PKCE challenge and nonce and returns a code.
func (f *fakeIdP) authorize(t *testing.T, authURL string) (code, state string) {
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, f.URL+"/authorize") {
		t.Fatalf("redirected to %q", authURL)
	}
	q := u.Query()
	if q.Get("prompt") != "login" || q.Get("response_type") != "code" || q.Get("client_id") != "orbit" ||
		q.Get("code_challenge_method") != "S256" || q.Get("scope") != "openid email profile" || q.Get("nonce") == "" {
		t.Fatalf("bad authorization request: %v", q)
	}
	code = newUUID()
	f.mu.Lock()
	f.codes[code] = [2]string{q.Get("code_challenge"), q.Get("nonce")}
	f.mu.Unlock()
	return code, q.Get("state")
}

func (f *fakeIdP) provider() *oidcProvider {
	return &oidcProvider{issuer: f.URL, clientID: "orbit", clientSecret: "s3cret", redirectURI: "http://orbit.test/api/v1/auth/oidc/callback", http: f.Client()}
}

func TestOIDCVerify(t *testing.T) {
	f := newFakeIdP(t)
	p := f.provider()
	ctx := context.Background()
	meta, err := p.discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	with := func(kv ...any) map[string]any { // with(k1, v1, k2, v2...); a nil value deletes the claim
		c := f.claims("n1")
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == nil {
				delete(c, kv[i].(string))
			} else {
				c[kv[i].(string)] = kv[i+1]
			}
		}
		return c
	}
	unsigned := func(alg string) string {
		tok := signJWT(t, f.rsa, alg, "rsa-1", f.claims("n1"))
		return tok[:strings.LastIndex(tok, ".")+1]
	}
	tamper := func(tok string) string {
		parts := strings.Split(tok, ".")
		parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"x"}`))
		return strings.Join(parts, ".")
	}
	for _, c := range []struct {
		name, token string
		ok          bool
	}{
		{"rsa", signJWT(t, f.rsa, "RS256", "rsa-1", f.claims("n1")), true},
		{"ecdsa", signJWT(t, f.ec, "ES256", "ec-1", f.claims("n1")), true},
		{"aud array with azp", signJWT(t, f.rsa, "RS256", "rsa-1", with("aud", []string{"orbit", "api"}, "azp", "orbit")), true},
		{"bad signature", signJWT(t, other, "RS256", "rsa-1", f.claims("n1")), false},
		{"tampered payload", tamper(signJWT(t, f.rsa, "RS256", "rsa-1", f.claims("n1"))), false},
		{"alg swapped to ES256 on RSA key", signJWT(t, f.ec, "ES256", "rsa-1", f.claims("n1")), false},
		{"alg none", unsigned("none"), false},
		{"HS256", signJWT(t, f.rsa, "HS256", "rsa-1", f.claims("n1")), false},
		{"encryption key", signJWT(t, f.rsa, "RS256", "enc-1", f.claims("n1")), false},
		{"wrong issuer", signJWT(t, f.rsa, "RS256", "rsa-1", with("iss", "https://evil.test")), false},
		{"wrong audience", signJWT(t, f.rsa, "RS256", "rsa-1", with("aud", "someone-else")), false},
		{"several audiences, no azp", signJWT(t, f.rsa, "RS256", "rsa-1", with("aud", []string{"orbit", "api"})), false},
		{"azp is another client", signJWT(t, f.rsa, "RS256", "rsa-1", with("azp", "other")), false},
		{"expired", signJWT(t, f.rsa, "RS256", "rsa-1", with("exp", now.Add(-3*time.Minute).Unix())), false},
		{"expired within leeway", signJWT(t, f.rsa, "RS256", "rsa-1", with("exp", now.Add(-time.Minute).Unix())), true},
		{"no exp", signJWT(t, f.rsa, "RS256", "rsa-1", with("exp", nil)), false},
		{"issued in the future", signJWT(t, f.rsa, "RS256", "rsa-1", with("iat", now.Add(10*time.Minute).Unix())), false},
		{"nonce mismatch", signJWT(t, f.rsa, "RS256", "rsa-1", f.claims("n2")), false},
		{"no nonce", signJWT(t, f.rsa, "RS256", "rsa-1", with("nonce", nil)), false},
		{"no subject", signJWT(t, f.rsa, "RS256", "rsa-1", with("sub", "")), false},
		{"garbage", "a.b", false},
	} {
		_, err := p.verify(ctx, meta, c.token, "n1", now)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v, err=%v", c.name, c.ok, err)
		}
	}
}

func TestOIDCKeyRotation(t *testing.T) {
	f := newFakeIdP(t)
	p := f.provider()
	ctx := context.Background()
	meta, _ := p.discover(ctx)
	for range 2 {
		if _, err := p.verify(ctx, meta, f.token(f.claims("n")), "n", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if f.jwksHits != 1 {
		t.Fatalf("JWKS fetched %d times, want 1 (cached)", f.jwksHits)
	}
	f.mu.Lock()
	f.rsa, _ = rsa.GenerateKey(rand.Reader, 2048)
	f.rsaID = "rsa-2"
	f.mu.Unlock()
	if _, err := p.verify(ctx, meta, f.token(f.claims("n")), "n", time.Now()); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	if f.jwksHits != 2 {
		t.Fatalf("unknown kid didn't refetch the JWKS: %d fetches", f.jwksHits)
	}
	if _, err := p.verify(ctx, meta, signJWT(t, f.rsa, "RS256", "nope", f.claims("n")), "n", time.Now()); err == nil {
		t.Fatal("token with an unknown kid accepted")
	}
}

func TestOIDCDiscoveryIssuerMismatch(t *testing.T) {
	f := newFakeIdP(t)
	p := f.provider()
	p.issuer = f.URL + "/" // a trailing slash is tolerated
	if _, err := p.discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	p = f.provider()
	p.issuer = f.URL + "/realms/other"
	if _, err := p.discover(context.Background()); err == nil {
		t.Fatal("discovery document for another issuer accepted")
	}
}

func TestOIDCExchange(t *testing.T) {
	f := newFakeIdP(t)
	p := f.provider()
	ctx := context.Background()
	meta, _ := p.discover(ctx)
	f.codes["c1"] = [2]string{base64.RawURLEncoding.EncodeToString(func() []byte { s := sha256.Sum256([]byte("verifier")); return s[:] }()), "n"}
	if _, err := p.exchange(ctx, meta, "c1", "wrong"); err == nil {
		t.Fatal("token endpoint accepted a wrong PKCE verifier")
	}
	f.codes["c1"] = [2]string{base64.RawURLEncoding.EncodeToString(func() []byte { s := sha256.Sum256([]byte("verifier")); return s[:] }()), "n"}
	if _, err := p.exchange(ctx, meta, "c1", "verifier"); err != nil {
		t.Fatal(err)
	}
	if f.lastForm.Get("code_verifier") != "verifier" || f.lastForm.Get("redirect_uri") != p.redirectURI || f.lastForm.Get("client_secret") != "" {
		t.Fatalf("token request: %v", f.lastForm)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("orbit:s3cret")); f.lastBasic != want {
		t.Fatalf("client auth %q, want %q", f.lastBasic, want)
	}
}

func TestEmailVerified(t *testing.T) {
	for _, c := range []struct {
		verified, edov any
		want           bool
	}{
		{true, nil, true}, {"true", nil, true}, {false, nil, false}, {nil, nil, false}, {"yes", nil, false},
		{nil, true, true}, {nil, "1", true}, {nil, false, false}, {false, true, true},
	} {
		if got := (&idClaims{EmailVerified: c.verified, EDOV: c.edov}).emailVerified(); got != c.want {
			t.Errorf("email_verified=%v xms_edov=%v: %v", c.verified, c.edov, got)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/p/1": "/p/1", "/invite/x?y=1": "/invite/x?y=1", "": "/", "//evil.test": "/",
		`/\evil.test`: "/", "https://evil.test": "/", "/\t/evil.test": "/", "javascript:alert(1)": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q", in, got)
		}
	}
}

// cookieServer is enough of a Server for the parts of the flow before the database.
func cookieServer(t *testing.T, f *fakeIdP) *Server {
	block, _ := aes.NewCipher(make([]byte, 32))
	aead, _ := cipher.NewGCM(block)
	return &Server{cfg: Config{AppURL: "http://orbit.test", OIDCName: "Okta"}, aead: aead, log: slog.New(slog.DiscardHandler), oidc: f.provider()}
}

// ssoStart begins a sign-in and returns the provider URL and the flow cookie.
func ssoStart(t *testing.T, h http.Handler, next string) (string, *http.Cookie) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/auth/oidc/start?next="+url.QueryEscape(next), nil))
	if w.Code != 302 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == oidcCookie {
			if !c.HttpOnly || c.Path != oidcPath || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("flow cookie attributes: %+v", c)
			}
			return w.Header().Get("Location"), c
		}
	}
	t.Fatal("no flow cookie")
	return "", nil
}

func ssoCallback(h http.Handler, query string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/v1/auth/oidc/callback?"+query, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestOIDCStateMismatch(t *testing.T) {
	f := newFakeIdP(t)
	s := cookieServer(t, f)
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/auth/oidc/start", s.h(s.oidcStart))
	mux.Handle("GET /api/v1/auth/oidc/callback", s.h(s.oidcCallback))
	authURL, cookie := ssoStart(t, mux, "/p/1")
	code, state := f.authorize(t, authURL)

	for name, w := range map[string]*httptest.ResponseRecorder{
		"wrong state":    ssoCallback(mux, url.Values{"code": {code}, "state": {state + "x"}}.Encode(), cookie),
		"no flow cookie": ssoCallback(mux, url.Values{"code": {code}, "state": {state}}.Encode()),
		"forged cookie":  ssoCallback(mux, url.Values{"code": {code}, "state": {state}}.Encode(), &http.Cookie{Name: oidcCookie, Value: "AAAA"}),
		"provider error": ssoCallback(mux, "error=access_denied&state="+state, cookie),
	} {
		if loc := w.Header().Get("Location"); w.Code != 302 || !strings.HasPrefix(loc, "/login?sso_error=") {
			t.Errorf("%s: %d %q", name, w.Code, loc)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie {
				t.Errorf("%s: session started", name)
			}
		}
	}
	if len(f.codes) != 1 {
		t.Fatal("code was redeemed despite the failed checks")
	}
}

// --- With a database ---

func ssoServer(t *testing.T) (*Server, *fakeIdP) {
	s := testServer(t)
	f := newFakeIdP(t)
	s.oidc, s.cfg.OIDCName = f.provider(), "Okta"
	return s, f
}

// ssoSignIn runs the whole browser flow and returns the callback response.
func ssoSignIn(t *testing.T, s *Server, f *fakeIdP, sub, email string) *httptest.ResponseRecorder {
	t.Helper()
	f.sub, f.email = sub, email
	h := s.Handler()
	authURL, cookie := ssoStart(t, h, "/p/next")
	code, state := f.authorize(t, authURL)
	return ssoCallback(h, url.Values{"code": {code}, "state": {state}}.Encode(), cookie)
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c
		}
	}
	return nil
}

func TestSSOSignIn(t *testing.T) {
	s, f := ssoServer(t)
	ctx := context.Background()
	uid, email := newTestUser(t, s, "sso", "a password 123")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	s.db.Exec(ctx, `insert into memberships values ($1,$2,'member')`, org, uid)
	sub := "sub-" + newUUID()

	w := ssoSignIn(t, s, f, sub, strings.ToUpper(email))
	if w.Code != 302 || w.Header().Get("Location") != "/p/next" || cookieNamed(w, sessionCookie) == nil {
		t.Fatalf("first sign-in: %d %q", w.Code, w.Header().Get("Location"))
	}
	var linked string
	s.db.QueryRow(ctx, `select oidc_subject from users where id = $1`, uid).Scan(&linked)
	if linked != sub {
		t.Fatalf("subject not linked: %q", linked)
	}
	var audited int
	s.db.QueryRow(ctx, `select count(*) from audit_events where org_id = $1 and action = 'user.sso_login'`, org).Scan(&audited)
	if audited != 1 {
		t.Fatalf("sign-in not audited: %d", audited)
	}

	// Same email, different person at the provider (address reassigned): refused and audited.
	w = ssoSignIn(t, s, f, "sub-"+newUUID(), email)
	if !strings.HasPrefix(w.Header().Get("Location"), "/login?sso_error=") || cookieNamed(w, sessionCookie) != nil {
		t.Fatalf("other subject signed in: %d %q", w.Code, w.Header().Get("Location"))
	}
	s.db.QueryRow(ctx, `select count(*) from audit_events where org_id = $1 and action = 'user.sso_failed'`, org).Scan(&audited)
	if audited != 1 {
		t.Fatalf("refusal not audited: %d", audited)
	}

	// The pinned subject still works after the provider changes the person's email.
	if w = ssoSignIn(t, s, f, sub, "renamed-"+newUUID()+"@orbit.test"); cookieNamed(w, sessionCookie) == nil {
		t.Fatalf("pinned subject with a new email: %q", w.Header().Get("Location"))
	}

	f.extra = map[string]any{"email_verified": false}
	if w = ssoSignIn(t, s, f, "sub-"+newUUID(), "new-"+newUUID()+"@orbit.test"); cookieNamed(w, sessionCookie) != nil {
		t.Fatal("unverified email signed in")
	}
	f.extra = nil
}

func TestSSOAccountCreation(t *testing.T) {
	s, f := ssoServer(t)
	ctx := context.Background()
	newTestUser(t, s, "existing", "x") // so this isn't the first-user case
	stranger := "stranger-" + newUUID() + "@orbit.test"
	if w := ssoSignIn(t, s, f, "sub-"+newUUID(), stranger); cookieNamed(w, sessionCookie) != nil {
		t.Fatal("closed sign-up created an account over SSO")
	}
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	invitee := "invitee-" + newUUID() + "@orbit.test"
	_, hash := newToken("orb_inv_")
	s.db.Exec(ctx, `insert into invites (org_id, email, role, token_hash, expires_at) values ($1,$2,'member',$3,now() + interval '1 day')`, org, invitee, hash)
	sub := "sub-" + newUUID()
	if w := ssoSignIn(t, s, f, sub, invitee); cookieNamed(w, sessionCookie) == nil {
		t.Fatalf("invitee couldn't sign up over SSO: %q", w.Header().Get("Location"))
	}
	var linked string
	if err := s.db.QueryRow(ctx, `select oidc_subject from users where email = $1`, invitee).Scan(&linked); err != nil || linked != sub {
		t.Fatalf("new account not linked: %q %v", linked, err)
	}
	s.cfg.AllowSignup = true
	if w := ssoSignIn(t, s, f, "sub-"+newUUID(), stranger); cookieNamed(w, sessionCookie) == nil {
		t.Fatal("open sign-up refused an SSO account")
	}
}

func TestSSOWithMFA(t *testing.T) {
	s, f := ssoServer(t)
	uid, email := newTestUser(t, s, "sso-mfa", "x")
	secret, _ := enableMFA(t, s, uid)
	w := ssoSignIn(t, s, f, "sub-"+newUUID(), email)
	pending := cookieNamed(w, oidcMFACookie)
	if w.Header().Get("Location") != "/login?sso=mfa&next=%2Fp%2Fnext" || cookieNamed(w, sessionCookie) != nil || pending == nil {
		t.Fatalf("SSO skipped the second factor: %q", w.Header().Get("Location"))
	}
	mfa := func(code string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/auth/oidc/mfa", strings.NewReader(`{"code":"`+code+`"}`))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := mfa("000000", pending); w.Code != 401 || cookieNamed(w, sessionCookie) != nil {
		t.Fatalf("wrong code: %d", w.Code)
	}
	code := totp(secret, time.Now().Unix()/30)
	if w := mfa(code, nil); w.Code != 401 {
		t.Fatalf("code accepted without the SSO step: %d", w.Code)
	}
	if w := mfa(code, pending); w.Code != 200 || cookieNamed(w, sessionCookie) == nil {
		t.Fatalf("valid code: %d %s", w.Code, w.Body)
	}
}

func TestRequireSSO(t *testing.T) {
	s, _ := ssoServer(t)
	ctx := context.Background()
	owner, ownerEmail := newTestUser(t, s, "owner", "a password 123")
	member, memberEmail := newTestUser(t, s, "member", "a password 123")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	s.db.Exec(ctx, `insert into memberships values ($1,$2,'owner')`, org, owner)
	s.db.Exec(ctx, `insert into memberships values ($1,$2,'admin')`, org, member)
	patch := func(uid string) int {
		r := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"require_sso":true}`))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("oid", org)
		w := httptest.NewRecorder()
		s.h(s.updateOrg)(w, r.WithContext(context.WithValue(ctx, userKey, user{ID: uid})))
		return w.Code
	}
	login := func(email string) int {
		st, _ := post(t, s, "/api/v1/auth/login", map[string]string{"email": email, "password": "a password 123"})
		return st
	}
	if st := patch(member); st != 403 {
		t.Fatalf("admin changed require_sso: %d", st)
	}
	if st := patch(owner); st != 204 {
		t.Fatalf("owner require_sso: %d", st)
	}
	if st := login(memberEmail); st != 403 {
		t.Fatalf("member signed in with a password: %d", st)
	}
	if st := login(ownerEmail); st != 200 {
		t.Fatalf("owner locked out: %d", st)
	}
	s.oidc = nil // SSO unconfigured: nobody stays locked out
	if st := login(memberEmail); st != 200 {
		t.Fatalf("member locked out without SSO configured: %d", st)
	}
	if st := patch(owner); st != 400 {
		t.Fatalf("require_sso turned on without SSO configured: %d", st)
	}
}
