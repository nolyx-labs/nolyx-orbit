// Sign-up, login, logout and the current user.
package server

import (
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("orbit-timing-equalizer"), 12)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Code     string `json:"code"`   // login: second factor
	Invite   string `json:"invite"` // signup: an invite token lets people join when sign-up is closed
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := decode(r, &c); err != nil {
		return err
	}
	key := "signup:" + clientIP(r)
	if s.attempts.blocked(key, 10) {
		return errStatus(429, "too many sign-up attempts; try again in 15 minutes")
	}
	s.attempts.fail(key)
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if !s.cfg.AllowSignup {
		// The first user can always sign up so a fresh self-hosted install is usable.
		var ok bool
		s.db.QueryRow(r.Context(), `select not exists(select 1 from users) or exists(select 1 from invites
			where token_hash = $1 and email = $2 and accepted_at is null and expires_at > now())`, hashToken(c.Invite), c.Email).Scan(&ok)
		if !ok {
			return errStatus(403, "sign up is disabled on this instance; ask an admin for an invitation")
		}
	}
	if _, err := mail.ParseAddress(c.Email); err != nil {
		return errStatus(400, "invalid email")
	}
	if err := validPassword(c.Password); err != nil {
		return err
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		c.Name = strings.Split(c.Email, "@")[0]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), 12)
	if err != nil {
		return err
	}
	var userID, orgID string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `insert into users (email, name, password_hash) values ($1,$2,$3) on conflict do nothing returning id::text`,
			c.Email, c.Name, string(hash)).Scan(&userID); err == pgx.ErrNoRows {
			return errStatus(409, "an account with that email already exists")
		} else if err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `insert into organizations (name) values ($1) returning id::text`, c.Name+"'s Infrastructure").Scan(&orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `insert into memberships values ($1,$2,'owner')`, orgID, userID); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `insert into projects (org_id, name) values ($1,'Production')`, orgID)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(r, orgID, "", userID, c.Email, "user.signup", c.Email, nil)
	return s.startSession(w, r, userID)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := decode(r, &c); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(c.Email))
	ipKey, emailKey := "ip:"+clientIP(r), "email:"+email
	if s.attempts.blocked(ipKey, 50) || s.attempts.blocked(emailKey, 10) {
		return errStatus(429, "too many failed sign-in attempts; try again in 15 minutes")
	}
	var id, hash string
	var mfa bool
	err := s.db.QueryRow(r.Context(), `select id::text, password_hash, totp_enabled_at is not null from users where email = $1`,
		email).Scan(&id, &hash, &mfa)
	if err == pgx.ErrNoRows {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password)) // equalize timing so emails can't be enumerated
		s.attempts.fail(ipKey, emailKey)
		return errStatus(401, "invalid email or password")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		s.attempts.fail(ipKey, emailKey)
		return errStatus(401, "invalid email or password")
	}
	if mfa {
		if c.Code == "" {
			writeJSON(w, 401, map[string]any{"error": "enter the code from your authenticator app", "mfa_required": true})
			return nil
		}
		if ok, err := s.verifyMFA(r.Context(), id, c.Code); err != nil {
			return err
		} else if !ok {
			s.attempts.fail(ipKey, emailKey)
			writeJSON(w, 401, map[string]any{"error": "invalid two-factor code", "mfa_required": true})
			return nil
		}
	}
	s.attempts.clear(emailKey)
	return s.startSession(w, r, id)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID string) error {
	token, hash := newToken("")
	exp := time.Now().Add(14 * 24 * time.Hour)
	if _, err := s.db.Exec(r.Context(), `insert into sessions (token_hash, user_id, expires_at) values ($1,$2,$3)`, hash, userID, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: exp, HttpOnly: true,
		Secure: strings.HasPrefix(s.cfg.AppURL, "https://"), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.db.Exec(r.Context(), `delete from sessions where token_hash = $1`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	orgs, err := s.list(r.Context(), `
		select o.id, o.name, m.role, o.require_mfa, mfa_blocked(o.id, m.user_id) as mfa_blocked,
		  (select coalesce(json_agg(json_build_object('id', p.id, 'name', p.name, 'role', greatest_role(m.role, (
		     select max(case tpr.role when 'viewer' then 0 when 'member' then 1 when 'admin' then 2 end)
		     from team_members tm join team_project_roles tpr on tpr.team_id = tm.team_id where tm.user_id = m.user_id and tpr.project_id = p.id)))
		   order by p.created_at), '[]')
		   from projects p where p.org_id = o.id) as projects
		from organizations o join memberships m on m.org_id = o.id
		where m.user_id = $1 order by o.created_at`, u.ID)
	if err != nil {
		return err
	}
	var mfa bool
	if err := s.db.QueryRow(r.Context(), `select totp_enabled_at is not null from users where id = $1`, u.ID).Scan(&mfa); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"user": map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "mfa_enabled": mfa}, "orgs": orgs})
	return nil
}
