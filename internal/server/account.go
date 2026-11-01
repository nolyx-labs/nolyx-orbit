// Account security: password change and reset, and two-factor authentication (TOTP + recovery codes).
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// totp is RFC 6238 with the parameters every authenticator app defaults to: SHA-1, 30s steps, 6 digits.
func totp(secret []byte, step int64) string {
	m := hmac.New(sha1.New, secret)
	binary.Write(m, binary.BigEndian, step)
	h := m.Sum(nil)
	o := h[len(h)-1] & 0xf
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(h[o:])&0x7fffffff)%1_000_000)
}

// totpMatch accepts the current step and one either side, for clock drift.
func totpMatch(secret []byte, code string, now time.Time) (int64, bool) {
	step := now.Unix() / 30
	for _, st := range []int64{step, step - 1, step + 1} {
		if subtle.ConstantTimeCompare([]byte(totp(secret, st)), []byte(code)) == 1 {
			return st, true
		}
	}
	return 0, false
}

func normalizeCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(code))
}

// verifyMFA accepts a current TOTP code or an unused recovery code, and burns either so it can't be replayed.
func (s *Server) verifyMFA(ctx context.Context, userID, code string) (bool, error) {
	code = normalizeCode(code)
	if len(code) == 6 {
		var enc []byte
		if err := s.db.QueryRow(ctx, `select totp_secret_encrypted from users where id = $1`, userID).Scan(&enc); err != nil || enc == nil {
			return false, err
		}
		secret, err := s.decrypt(enc)
		if err != nil {
			return false, err
		}
		step, ok := totpMatch(secret, code, time.Now())
		if !ok {
			return false, nil
		}
		tag, err := s.db.Exec(ctx, `update users set totp_last_step = $2 where id = $1 and totp_last_step < $2`, userID, step)
		return err == nil && tag.RowsAffected() == 1, err
	}
	tag, err := s.db.Exec(ctx, `update recovery_codes set used_at = now() where user_id = $1 and code_hash = $2 and used_at is null`,
		userID, hashToken(code))
	return err == nil && tag.RowsAffected() == 1, err
}

// newRecoveryCodes replaces the user's recovery codes; only hashes are stored.
func newRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	if _, err := tx.Exec(ctx, `delete from recovery_codes where user_id = $1`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 7)
		rand.Read(b)
		c := strings.ToLower(b32.EncodeToString(b))[:10]
		codes[i] = c[:5] + "-" + c[5:]
		if _, err := tx.Exec(ctx, `insert into recovery_codes (user_id, code_hash) values ($1,$2)`, userID, hashToken(c)); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// sessionUser rejects API keys: account security changes need the person, not a token.
func sessionUser(r *http.Request) (user, error) {
	u := currentUser(r)
	if u.Key != nil {
		return u, errStatus(403, "sign in to change account security settings")
	}
	return u, nil
}

// checkPassword re-verifies the signed-in user's password; failures count toward the sign-in limit.
func (s *Server) checkPassword(ctx context.Context, u user, password string) error {
	key := "email:" + u.Email
	if s.attempts.blocked(key, 10) {
		return errStatus(429, "too many failed attempts; try again in 15 minutes")
	}
	var hash string
	if err := s.db.QueryRow(ctx, `select password_hash from users where id = $1`, u.ID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.attempts.fail(key)
		return errStatus(403, "incorrect password")
	}
	return nil
}

// requireOwnMFA stops an admin from requiring 2FA (and locking themselves out) before turning it on themselves.
func (s *Server) requireOwnMFA(r *http.Request) error {
	var on bool
	if err := s.db.QueryRow(r.Context(), `select totp_enabled_at is not null from users where id = $1`, currentUser(r).ID).Scan(&on); err != nil {
		return err
	}
	if !on {
		return errStatus(400, "turn on two-factor authentication for your own account first (Settings → Account)")
	}
	return nil
}

func validPassword(p string) error {
	if len(p) < 10 || len(p) > 72 {
		return errStatus(400, "password must be 10–72 characters")
	}
	return nil
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	u, err := sessionUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := validPassword(in.New); err != nil {
		return err
	}
	if err := s.checkPassword(r.Context(), u, in.Current); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		return err
	}
	c, _ := r.Cookie(sessionCookie)
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update users set password_hash = $2 where id = $1`, u.ID, string(hash)); err != nil {
			return err
		}
		// Sign out everywhere else, in case the old password leaked.
		_, err := tx.Exec(r.Context(), `delete from sessions where user_id = $1 and token_hash <> $2`, u.ID, hashToken(c.Value))
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) mfaSetup(w http.ResponseWriter, r *http.Request) error {
	u, err := sessionUser(r)
	if err != nil {
		return err
	}
	secret := make([]byte, 20)
	rand.Read(secret)
	tag, err := s.db.Exec(r.Context(), `update users set totp_secret_encrypted = $2 where id = $1 and totp_enabled_at is null`, u.ID, s.encrypt(secret))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errStatus(409, "two-factor authentication is already on")
	}
	key := b32.EncodeToString(secret)
	uri := url.URL{Scheme: "otpauth", Host: "totp", Path: "/Nolyx Orbit:" + u.Email,
		RawQuery: url.Values{"secret": {key}, "issuer": {"Nolyx Orbit"}}.Encode()}
	writeJSON(w, 200, map[string]string{"secret": key, "uri": uri.String()})
	return nil
}

func (s *Server) mfaEnable(w http.ResponseWriter, r *http.Request) error {
	u, err := sessionUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var pending bool
	if err := s.db.QueryRow(r.Context(), `select totp_secret_encrypted is not null and totp_enabled_at is null from users where id = $1`,
		u.ID).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return errStatus(409, "start two-factor setup first")
	}
	if ok, err := s.verifyMFA(r.Context(), u.ID, in.Code); err != nil {
		return err
	} else if !ok {
		return errStatus(400, "that code didn't match; check the time on your device and try again")
	}
	var codes []string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update users set totp_enabled_at = now() where id = $1`, u.ID); err != nil {
			return err
		}
		codes, err = newRecoveryCodes(r.Context(), tx, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"recovery_codes": codes})
	return nil
}

// mfaGate checks password (when given) and a second-factor code for disabling 2FA or new recovery codes.
func (s *Server) mfaGate(r *http.Request, u user, password *string, code string) error {
	if password != nil {
		if err := s.checkPassword(r.Context(), u, *password); err != nil {
			return err
		}
	}
	ok, err := s.verifyMFA(r.Context(), u.ID, code)
	if err != nil {
		return err
	}
	if !ok {
		s.attempts.fail("email:" + u.Email)
		return errStatus(403, "invalid two-factor code")
	}
	return nil
}

func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request) error {
	u, err := sessionUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	var org string
	err = s.db.QueryRow(r.Context(), `select o.name from memberships m join organizations o on o.id = m.org_id
		where m.user_id = $1 and mfa_required(m.org_id, m.user_id) order by o.name limit 1`, u.ID).Scan(&org)
	if err == nil {
		return errStatus(409, "%s requires two-factor authentication, so it can't be turned off", org)
	} else if err != pgx.ErrNoRows {
		return err
	}
	if err := s.mfaGate(r, u, &in.Password, in.Code); err != nil {
		return err
	}
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update users set totp_secret_encrypted = null, totp_enabled_at = null where id = $1`, u.ID); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `delete from recovery_codes where user_id = $1`, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) mfaRecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	u, err := sessionUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := s.mfaGate(r, u, nil, in.Code); err != nil {
		return err
	}
	var codes []string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		codes, err = newRecoveryCodes(r.Context(), tx, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"recovery_codes": codes})
	return nil
}

func (s *Server) newPasswordReset(ctx context.Context, userID string, recovery bool) (string, error) {
	token, hash := newToken("orb_pwr_")
	_, err := s.db.Exec(ctx, `insert into password_resets (token_hash, user_id, expires_at, recovery) values ($1,$2,now() + interval '1 hour',$3)`,
		hash, userID, recovery)
	return s.cfg.AppURL + "/reset-password/" + token, err
}

// RecoveryLink is for an operator with shell access (orbit reset-password): a reset link that also turns off 2FA.
// It doesn't use New, which would mark every server offline under a running control plane.
func RecoveryLink(ctx context.Context, cfg Config, email string) (string, error) {
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return "", err
	}
	defer db.Close()
	s := &Server{cfg: cfg, db: db}
	var id string
	if err := s.db.QueryRow(ctx, `select id::text from users where email = $1`, strings.ToLower(strings.TrimSpace(email))).Scan(&id); err != nil {
		return "", fmt.Errorf("no account for %s: %w", email, err)
	}
	return s.newPasswordReset(ctx, id, true)
}

func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		return errStatus(503, "email isn't set up on this instance; ask your Orbit operator to run: orbit reset-password <email>")
	}
	key := "reset:" + clientIP(r)
	if s.attempts.blocked(key, 5) {
		return errStatus(429, "too many reset requests; try again in 15 minutes")
	}
	s.attempts.fail(key)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	var id string
	if s.db.QueryRow(r.Context(), `select id::text from users where email = $1`, email).Scan(&id) == nil {
		link, err := s.newPasswordReset(r.Context(), id, false)
		if err != nil {
			return err
		}
		// Send in the background so the response time doesn't reveal whether the account exists.
		go func() {
			if err := s.sendTemplate([]string{email}, "Reset your Nolyx Orbit password", "password-reset", map[string]string{"Link": link}); err != nil {
				s.log.Warn("password reset email failed", "err", err)
			}
		}()
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) confirmPasswordReset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := validPassword(in.Password); err != nil {
		return err
	}
	var userID, email string
	var mfa, recovery bool
	err := s.db.QueryRow(r.Context(), `select u.id::text, u.email, u.totp_enabled_at is not null, pr.recovery
		from password_resets pr join users u on u.id = pr.user_id
		where pr.token_hash = $1 and pr.used_at is null and pr.expires_at > now()`, hashToken(in.Token)).Scan(&userID, &email, &mfa, &recovery)
	if err == pgx.ErrNoRows {
		return errStatus(404, "this reset link is invalid or has expired")
	} else if err != nil {
		return err
	}
	// An emailed link alone must not bypass 2FA: a stolen mailbox shouldn't be enough.
	if mfa && !recovery {
		key := "email:" + email
		if s.attempts.blocked(key, 10) {
			return errStatus(429, "too many failed attempts; try again in 15 minutes")
		}
		if in.Code == "" {
			writeJSON(w, 401, map[string]any{"error": "enter the code from your authenticator app", "mfa_required": true})
			return nil
		}
		if ok, err := s.verifyMFA(r.Context(), userID, in.Code); err != nil {
			return err
		} else if !ok {
			s.attempts.fail(key)
			writeJSON(w, 401, map[string]any{"error": "invalid two-factor code", "mfa_required": true})
			return nil
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `update password_resets set used_at = now() where token_hash = $1 and used_at is null`, hashToken(in.Token))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errStatus(404, "this reset link is invalid or has expired")
		}
		if _, err := tx.Exec(r.Context(), `update users set password_hash = $2 where id = $1`, userID, string(hash)); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `delete from sessions where user_id = $1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `delete from password_resets where user_id = $1 and used_at is null`, userID); err != nil {
			return err
		}
		if recovery {
			if _, err := tx.Exec(r.Context(), `update users set totp_secret_encrypted = null, totp_enabled_at = null where id = $1`, userID); err != nil {
				return err
			}
			_, err = tx.Exec(r.Context(), `delete from recovery_codes where user_id = $1`, userID)
		}
		return err
	})
	if err != nil {
		return err
	}
	s.attempts.clear("email:" + email)
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
