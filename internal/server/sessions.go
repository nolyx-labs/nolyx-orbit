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
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := decode(r, &c); err != nil {
		return err
	}
	if !s.cfg.AllowSignup {
		var n int
		s.db.QueryRow(r.Context(), `select count(*) from users`).Scan(&n)
		if n > 0 { // the first user can always sign up so a fresh self-hosted install is usable
			return errStatus(403, "sign up is disabled on this instance")
		}
	}
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if _, err := mail.ParseAddress(c.Email); err != nil {
		return errStatus(400, "invalid email")
	}
	if len(c.Password) < 10 || len(c.Password) > 72 {
		return errStatus(400, "password must be 10–72 characters")
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
	var id, hash string
	err := s.db.QueryRow(r.Context(), `select id::text, password_hash from users where email = $1`,
		strings.ToLower(strings.TrimSpace(c.Email))).Scan(&id, &hash)
	if err == pgx.ErrNoRows {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password)) // equalize timing so emails can't be enumerated
		return errStatus(401, "invalid email or password")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		return errStatus(401, "invalid email or password")
	}
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
		select o.id, o.name, m.role,
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
	writeJSON(w, 200, map[string]any{"user": map[string]string{"id": u.ID, "email": u.Email, "name": u.Name}, "orgs": orgs})
	return nil
}
