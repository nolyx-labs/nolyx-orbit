// Single sign-on with one instance-wide OpenID Connect provider: the authorization code flow with PKCE,
// with ID tokens verified here using only the standard library.
package server

import (
	"cmp"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	oidcCookie    = "orbit_oidc"     // state, nonce and PKCE verifier of one sign-in attempt
	oidcMFACookie = "orbit_oidc_mfa" // signed in with SSO, waiting for the two-factor code
	oidcPath      = "/api/v1/auth/oidc"
	oidcLeeway    = 2 * time.Minute
	oidcCacheTTL  = time.Hour
)

type oidcProvider struct {
	issuer, clientID, clientSecret, redirectURI string
	http                                        *http.Client

	mu     sync.Mutex
	meta   *oidcMeta
	metaAt time.Time
	keys   map[string]crypto.PublicKey // by kid
	keysAt time.Time
}

type oidcMeta struct {
	Issuer        string   `json:"issuer"`
	AuthEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint string   `json:"token_endpoint"`
	JWKSURI       string   `json:"jwks_uri"`
	AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
}

func newOIDC(cfg Config) (*oidcProvider, error) {
	if cfg.OIDCIssuer == "" {
		return nil, nil
	}
	if cfg.OIDCClientID == "" {
		return nil, errors.New("OIDC_CLIENT_ID is required when OIDC_ISSUER is set")
	}
	return &oidcProvider{issuer: cfg.OIDCIssuer, clientID: cfg.OIDCClientID, clientSecret: cfg.OIDCClientSecret,
		redirectURI: cfg.AppURL + oidcPath + "/callback", http: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (p *oidcProvider) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: status %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

func (p *oidcProvider) discover(ctx context.Context) (*oidcMeta, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.meta != nil && time.Since(p.metaAt) < oidcCacheTTL {
		return p.meta, nil
	}
	var m oidcMeta
	if err := p.getJSON(ctx, strings.TrimSuffix(p.issuer, "/")+"/.well-known/openid-configuration", &m); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	// Providers differ on a trailing slash (Auth0 has one); otherwise it must be the issuer that was configured.
	if strings.TrimSuffix(m.Issuer, "/") != strings.TrimSuffix(p.issuer, "/") || m.AuthEndpoint == "" || m.TokenEndpoint == "" || m.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery: issuer %q doesn't match OIDC_ISSUER, or endpoints are missing", m.Issuer)
	}
	p.meta, p.metaAt = &m, time.Now()
	return &m, nil
}

// key returns the signing key for kid, refetching the JWKS when kid is unknown (the provider rotated keys).
// ID tokens only arrive from the token endpoint, never from the browser, so nobody can force refetches at will.
func (p *oidcProvider) key(ctx context.Context, jwksURI, kid string) (crypto.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if k := p.keys[kid]; k != nil && time.Since(p.keysAt) < oidcCacheTTL {
		return k, nil
	}
	var set struct {
		Keys []struct{ Kty, Kid, Use, Crv, N, E, X, Y string } `json:"keys"`
	}
	if err := p.getJSON(ctx, jwksURI, &set); err != nil {
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	p.keys, p.keysAt = map[string]crypto.PublicKey{}, time.Now()
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if pub, err := parseJWK(k.Kty, k.Crv, k.N, k.E, k.X, k.Y); err == nil {
			p.keys[k.Kid] = pub
		}
	}
	if k := p.keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("oidc: no signing key %q in the provider's JWKS", kid)
}

func parseJWK(kty, crv, n, e, x, y string) (crypto.PublicKey, error) {
	dec := base64.RawURLEncoding.DecodeString
	switch kty {
	case "RSA":
		nb, err1 := dec(n)
		eb, err2 := dec(e)
		if err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 {
			return nil, errors.New("bad RSA key")
		}
		k := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(new(big.Int).SetBytes(eb).Int64())}
		if k.N.BitLen() < 2048 {
			return nil, errors.New("RSA key too small")
		}
		return k, nil
	case "EC":
		xb, err1 := dec(x)
		yb, err2 := dec(y)
		if crv != "P-256" || err1 != nil || err2 != nil || len(xb) != 32 || len(yb) != 32 {
			return nil, errors.New("unsupported EC key")
		}
		return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, xb...), yb...)) // checks the point is on the curve
	}
	return nil, errors.New("unsupported key type")
}

type idClaims struct {
	Iss           string   `json:"iss"`
	Sub           string   `json:"sub"`
	Aud           audience `json:"aud"`
	Azp           string   `json:"azp"`
	Exp           float64  `json:"exp"`
	Iat           float64  `json:"iat"`
	Nonce         string   `json:"nonce"`
	Email         string   `json:"email"`
	EmailVerified any      `json:"email_verified"`
	EDOV          any      `json:"xms_edov"`
	Name          string   `json:"name"`
}

// audience is a single string or an array (RFC 7519 §4.1.3).
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(a))
}

// emailVerified: Microsoft Entra ID sends no email_verified, and its email claim can be any address a user typed
// into their profile. So for Entra we trust the email only with the optional xms_edov claim (the tenant owns the domain).
func (c *idClaims) emailVerified() bool { return truthy(c.EmailVerified) || truthy(c.EDOV) }

func truthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	case float64:
		return v == 1
	}
	return false
}

// verify checks an ID token's signature and claims (OpenID Connect Core §3.1.3.7).
func (p *oidcProvider) verify(ctx context.Context, meta *oidcMeta, raw, nonce string, now time.Time) (*idClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	var hdr struct{ Alg, Kid string }
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &hdr) != nil {
		return nil, errors.New("malformed ID token header")
	}
	if hdr.Alg != "RS256" && hdr.Alg != "ES256" { // also rejects "none" and HMAC
		return nil, fmt.Errorf("unsupported ID token algorithm %q", hdr.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed ID token signature")
	}
	key, err := p.key(ctx, meta.JWKSURI, hdr.Kid)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	ok := false
	// The key type must match alg, or the token could choose how it's verified.
	switch k := key.(type) {
	case *rsa.PublicKey:
		ok = hdr.Alg == "RS256" && rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
	case *ecdsa.PublicKey:
		ok = hdr.Alg == "ES256" && len(sig) == 64 && ecdsa.Verify(k, h[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
	}
	if !ok {
		return nil, errors.New("ID token signature is invalid")
	}
	var c idClaims
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(pb, &c) != nil {
		return nil, errors.New("malformed ID token claims")
	}
	switch {
	case c.Iss != meta.Issuer:
		return nil, fmt.Errorf("ID token issuer %q is not %q", c.Iss, meta.Issuer)
	case !slices.Contains(c.Aud, p.clientID):
		return nil, errors.New("ID token is for another client")
	case (len(c.Aud) > 1 || c.Azp != "") && c.Azp != p.clientID:
		return nil, errors.New("ID token authorized party is another client")
	case c.Exp == 0 || now.After(time.Unix(int64(c.Exp), 0).Add(oidcLeeway)):
		return nil, errors.New("ID token expired")
	case c.Iat == 0 || time.Unix(int64(c.Iat), 0).After(now.Add(oidcLeeway)):
		return nil, errors.New("ID token issue time is missing or in the future")
	case nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1:
		return nil, errors.New("ID token nonce doesn't match")
	case c.Sub == "":
		return nil, errors.New("ID token has no subject")
	}
	return &c, nil
}

// exchange trades the authorization code (plus the PKCE verifier) for an ID token.
func (p *oidcProvider) exchange(ctx context.Context, meta *oidcMeta, code, verifier string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.redirectURI}, "code_verifier": {verifier}}
	// client_secret_basic is the spec default; fall back to client_secret_post for providers that don't offer it.
	basic := p.clientSecret != "" && (len(meta.AuthMethods) == 0 || slices.Contains(meta.AuthMethods, "client_secret_basic"))
	if !basic {
		form.Set("client_id", p.clientID)
		if p.clientSecret != "" {
			form.Set("client_secret", p.clientSecret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(p.clientID), url.QueryEscape(p.clientSecret)) // RFC 6749 §2.3.1 form-encodes both
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc token endpoint: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != 200 || out.IDToken == "" {
		return "", fmt.Errorf("oidc token endpoint: status %d %s %s", resp.StatusCode, out.Error, trunc(out.Desc, 200))
	}
	return out.IDToken, nil
}

type oidcFlow struct {
	State, Nonce, Verifier, Next string
	Exp                          int64
}

type oidcPending struct {
	UserID string
	Exp    int64
}

// sealCookie stores v encrypted and authenticated, so it can be neither read nor forged.
func (s *Server) sealCookie(w http.ResponseWriter, name string, v any, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: base64.RawURLEncoding.EncodeToString(s.encrypt(mustJSON(v))), Path: oidcPath,
		MaxAge: int(ttl.Seconds()), HttpOnly: true, Secure: strings.HasPrefix(s.cfg.AppURL, "https://"), SameSite: http.SameSiteLaxMode})
}

func (s *Server) openCookie(r *http.Request, name string, v any) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return false
	}
	plain, err := s.decrypt(b)
	return err == nil && json.Unmarshal(plain, v) == nil
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: oidcPath, MaxAge: -1})
}

// safeNext keeps the post-sign-in redirect on this site; "//host" and "/\host" would leave it.
func safeNext(next string) string {
	if _, err := url.Parse(next); err != nil || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, `\`) {
		return "/"
	}
	return next
}

func (s *Server) oidcInfo(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, map[string]any{"enabled": s.oidc != nil, "name": s.cfg.OIDCName})
	return nil
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) error {
	if s.oidc == nil {
		return errStatus(404, "single sign-on isn't set up on this instance")
	}
	meta, err := s.oidc.discover(r.Context())
	if err != nil {
		return s.ssoFailed(w, r, "", "", err)
	}
	u, err := url.Parse(meta.AuthEndpoint)
	if err != nil {
		return s.ssoFailed(w, r, "", "", err)
	}
	f := oidcFlow{Next: safeNext(r.URL.Query().Get("next")), Exp: time.Now().Add(10 * time.Minute).Unix()}
	f.State, _ = newToken("")
	f.Nonce, _ = newToken("")
	f.Verifier, _ = newToken("")
	s.sealCookie(w, oidcCookie, f, 10*time.Minute)
	challenge := sha256.Sum256([]byte(f.Verifier))
	q := u.Query()
	for k, v := range map[string]string{"response_type": "code", "client_id": s.oidc.clientID, "redirect_uri": s.oidc.redirectURI,
		"scope": "openid email profile", "state": f.State, "nonce": f.Nonce,
		"code_challenge": base64.RawURLEncoding.EncodeToString(challenge[:]), "code_challenge_method": "S256"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
	return nil
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) error {
	if s.oidc == nil {
		return errStatus(404, "single sign-on isn't set up on this instance")
	}
	var f oidcFlow
	ok := s.openCookie(r, oidcCookie, &f) && time.Now().Unix() <= f.Exp
	clearCookie(w, oidcCookie) // one attempt per cookie
	if !ok {
		return s.ssoFailed(w, r, "", "", errStatus(400, "this sign-in expired or was started in another browser; try again"))
	}
	uid, email, err := s.oidcIdentity(r, f)
	if err != nil {
		return s.ssoFailed(w, r, uid, email, err)
	}
	var mfa bool
	if err := s.db.QueryRow(r.Context(), `select totp_enabled_at is not null from users where id = $1`, uid).Scan(&mfa); err != nil {
		return err
	}
	if mfa { // SSO replaces the password, not the second factor: the sign-in page asks for the code next
		s.sealCookie(w, oidcMFACookie, oidcPending{UserID: uid, Exp: time.Now().Add(5 * time.Minute).Unix()}, 5*time.Minute)
		http.Redirect(w, r, "/login?sso=mfa&next="+url.QueryEscape(f.Next), http.StatusFound)
		return nil
	}
	if err := s.newSession(w, r, uid); err != nil {
		return err
	}
	s.auditUser(r, uid, email, "user.sso_login", nil)
	http.Redirect(w, r, f.Next, http.StatusFound)
	return nil
}

// oidcIdentity completes the code flow and returns the account it signs in to. uid is also set
// when a verified identity was refused for a known account, so the refusal can be audited.
func (s *Server) oidcIdentity(r *http.Request, f oidcFlow) (uid, email string, err error) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return "", "", errStatus(400, "your sign-in provider refused: %s", trunc(cmp.Or(q.Get("error_description"), e), 200))
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		return "", "", errStatus(400, "this sign-in doesn't match the one started in this browser; try again")
	}
	meta, err := s.oidc.discover(r.Context())
	if err != nil {
		return "", "", err
	}
	raw, err := s.oidc.exchange(r.Context(), meta, q.Get("code"), f.Verifier)
	if err != nil {
		return "", "", err
	}
	c, err := s.oidc.verify(r.Context(), meta, raw, f.Nonce, time.Now())
	if err != nil {
		return "", "", err
	}
	email = strings.ToLower(strings.TrimSpace(c.Email))
	uid, err = s.ssoUser(r, meta.Issuer, email, c)
	return uid, email, err
}

// ssoUser finds or creates the account for a verified identity. The provider's subject identifies the person;
// the email only links an existing account the first time, after which the subject must match.
func (s *Server) ssoUser(r *http.Request, iss, email string, c *idClaims) (string, error) {
	ctx := r.Context()
	if _, err := mail.ParseAddress(email); err != nil {
		return "", errStatus(403, "your sign-in provider didn't share an email address")
	}
	if !c.emailVerified() {
		return "", errStatus(403, "your sign-in provider hasn't verified %s", email)
	}
	var id, sub string
	if err := s.db.QueryRow(ctx, `select id::text from users where oidc_issuer = $1 and oidc_subject = $2`, iss, c.Sub).Scan(&id); err == nil {
		return id, nil
	} else if err != pgx.ErrNoRows {
		return "", err
	}
	err := s.db.QueryRow(ctx, `select id::text, coalesce(oidc_subject, '') from users where email = $1`, email).Scan(&id, &sub)
	switch {
	case err == nil && sub != "":
		return id, errStatus(403, "%s is linked to a different single sign-on identity", email)
	case err == nil:
		tag, err := s.db.Exec(ctx, `update users set oidc_issuer = $2, oidc_subject = $3, oidc_linked_at = now() where id = $1 and oidc_subject is null`,
			id, iss, c.Sub)
		if err == nil && tag.RowsAffected() == 0 {
			err = errStatus(409, "sign-in conflicted with another one; try again")
		}
		return id, err
	case err != pgx.ErrNoRows:
		return "", err
	}
	if !s.signupAllowed(ctx, email, nil) {
		return "", errStatus(403, "there's no Orbit account for %s; ask an admin for an invitation", email)
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	// An unknowable password: they can set a real one with "Forgot password?" if they ever need it.
	hash, err := bcrypt.GenerateFromPassword([]byte(randomSecret()), 12)
	if err != nil {
		return "", err
	}
	id, orgID, err := s.createAccount(ctx, email, name, string(hash))
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `update users set oidc_issuer = $2, oidc_subject = $3, oidc_linked_at = now() where id = $1`, id, iss, c.Sub); err != nil {
		return "", err
	}
	s.audit(r, orgID, "", id, email, "user.signup", email, map[string]any{"sso": true})
	return id, nil
}

// ssoFailed logs why an SSO sign-in failed and sends the browser back to the sign-in page. Policy refusals
// (apiError) are shown as is; anything else is a provider or configuration problem, detailed only in the log.
func (s *Server) ssoFailed(w http.ResponseWriter, r *http.Request, uid, email string, err error) error {
	s.log.Warn("sso sign-in failed", "email", email, "err", err)
	if uid != "" {
		s.auditUser(r, uid, email, "user.sso_failed", map[string]any{"reason": err.Error()})
	}
	msg := "single sign-on failed; ask your Orbit admin to check the server logs"
	var ae apiError
	if errors.As(err, &ae) {
		msg = ae.msg
	}
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
	return nil
}

// auditUser records a sign-in event in each of the user's organizations, since audit logs are per organization.
func (s *Server) auditUser(r *http.Request, uid, email, action string, meta map[string]any) {
	rows, _ := s.db.Query(r.Context(), `select org_id::text from memberships where user_id = $1`, uid)
	orgs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.log.Error("AUDIT WRITE FAILED", "action", action, "target", email, "err", err)
	}
	for _, o := range orgs {
		s.audit(r, o, "", uid, email, action, email, meta)
	}
}

// oidcMFA is the second step after SSO for users with two-factor authentication, like the password login's.
func (s *Server) oidcMFA(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var p oidcPending
	if !s.openCookie(r, oidcMFACookie, &p) || p.UserID == "" || time.Now().Unix() > p.Exp {
		return errStatus(401, "this sign-in expired; sign in again")
	}
	var email string
	if err := s.db.QueryRow(r.Context(), `select email from users where id = $1`, p.UserID).Scan(&email); err != nil {
		return err
	}
	ipKey, emailKey := "ip:"+clientIP(r), "email:"+email
	if s.attempts.blocked(ipKey, 50) || s.attempts.blocked(emailKey, 10) {
		return errStatus(429, "too many failed sign-in attempts; try again in 15 minutes")
	}
	if in.Code == "" {
		writeJSON(w, 401, map[string]any{"error": "enter the code from your authenticator app", "mfa_required": true})
		return nil
	}
	if ok, err := s.verifyMFA(r.Context(), p.UserID, in.Code); err != nil {
		return err
	} else if !ok {
		s.attempts.fail(ipKey, emailKey)
		writeJSON(w, 401, map[string]any{"error": "invalid two-factor code", "mfa_required": true})
		return nil
	}
	s.attempts.clear(emailKey)
	clearCookie(w, oidcMFACookie)
	s.auditUser(r, p.UserID, email, "user.sso_login", nil)
	return s.startSession(w, r, p.UserID)
}

// passwordBlockedBySSO names an organization whose "Require single sign-on" stops this user signing in with a
// password. Owners are exempt so a provider outage can't lock everyone out, and so is everyone once SSO is unconfigured.
func (s *Server) passwordBlockedBySSO(ctx context.Context, uid string) (string, error) {
	if s.oidc == nil {
		return "", nil
	}
	var org string
	err := s.db.QueryRow(ctx, `select o.name from memberships m join organizations o on o.id = m.org_id
		where m.user_id = $1 and o.require_sso and m.role <> 'owner' order by o.name limit 1`, uid).Scan(&org)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return org, err
}

func (s *Server) accountSSO(w http.ResponseWriter, r *http.Request) error {
	out, err := s.one(r.Context(), `select $2::boolean as enabled, $3::text as name, oidc_subject as subject, oidc_linked_at as linked_at
		from users where id = $1`, currentUser(r).ID, s.oidc != nil, s.cfg.OIDCName)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
