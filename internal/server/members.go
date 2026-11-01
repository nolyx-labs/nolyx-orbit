// Organization members and invitations.
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) error {
	oid, myRole, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if _, ok := roleRank[in.Role]; !ok {
		return errStatus(400, "invalid role")
	}
	uid := r.PathValue("uid")
	var current, email string
	if err := s.db.QueryRow(r.Context(), `select m.role, u.email from memberships m join users u on u.id = m.user_id where m.org_id::text = $1 and m.user_id::text = $2`,
		oid, uid).Scan(&current, &email); err != nil {
		return err
	}
	// Only owners can create or demote owners; nobody can act above their own role.
	if (in.Role == "owner" || current == "owner") && myRole != "owner" {
		return errStatus(403, "only owners can change owner roles")
	}
	if current == "owner" && in.Role != "owner" {
		if err := s.keepAnOwner(r.Context(), oid); err != nil {
			return err
		}
	}
	s.db.Exec(r.Context(), `update memberships set role = $3 where org_id::text = $1 and user_id::text = $2`, oid, uid, in.Role)
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "member.role", email, map[string]any{"from": current, "to": in.Role})
	w.WriteHeader(204)
	return nil
}

func (s *Server) keepAnOwner(ctx context.Context, oid string) error {
	var owners int
	s.db.QueryRow(ctx, `select count(*) from memberships where org_id::text = $1 and role = 'owner'`, oid).Scan(&owners)
	if owners <= 1 {
		return errStatus(409, "an organization needs at least one owner")
	}
	return nil
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) error {
	uid := r.PathValue("uid")
	minRole := "admin"
	if uid == currentUser(r).ID {
		minRole = "viewer" // anyone can leave
	}
	oid, myRole, err := s.orgAccess(r, minRole)
	if err != nil {
		return err
	}
	var role, email string
	if err := s.db.QueryRow(r.Context(), `select m.role, u.email from memberships m join users u on u.id = m.user_id where m.org_id::text = $1 and m.user_id::text = $2`,
		oid, uid).Scan(&role, &email); err != nil {
		return err
	}
	if role == "owner" {
		if myRole != "owner" {
			return errStatus(403, "only owners can remove owners")
		}
		if err := s.keepAnOwner(r.Context(), oid); err != nil {
			return err
		}
	}
	s.db.Exec(r.Context(), `delete from team_members where user_id::text = $2 and team_id in (select id from teams where org_id::text = $1)`, oid, uid)
	s.db.Exec(r.Context(), `delete from memberships where org_id::text = $1 and user_id::text = $2`, oid, uid)
	s.db.Exec(r.Context(), `update api_keys set revoked_at = now() where org_id::text = $1 and created_by::text = $2 and revoked_at is null`, oid, uid)
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "member.remove", email, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) error {
	oid, myRole, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if _, err := mail.ParseAddress(in.Email); err != nil {
		return errStatus(400, "invalid email")
	}
	if in.Role != "admin" && in.Role != "member" && in.Role != "viewer" {
		return errStatus(400, "role must be admin, member or viewer")
	}
	if roleRank[in.Role] > roleRank[myRole] {
		return errStatus(403, "you can't invite above your own role")
	}
	var exists bool
	s.db.QueryRow(r.Context(), `select exists(select 1 from memberships m join users u on u.id = m.user_id where m.org_id::text = $1 and u.email = $2)`, oid, in.Email).Scan(&exists)
	if exists {
		return errStatus(409, "%s is already a member", in.Email)
	}
	token, hash := newToken("orb_inv_")
	if _, err := s.db.Exec(r.Context(), `insert into invites (org_id, email, role, token_hash, invited_by, expires_at) values ($1,$2,$3,$4,$5,$6)`,
		oid, in.Email, in.Role, hash, currentUser(r).ID, time.Now().Add(7*24*time.Hour)); err != nil {
		return err
	}
	link := s.cfg.AppURL + "/invite/" + token
	var orgName string
	s.db.QueryRow(r.Context(), `select name from organizations where id::text = $1`, oid).Scan(&orgName)
	emailed := s.sendEmail([]string{in.Email}, "You're invited to "+orgName+" on Nolyx Orbit",
		fmt.Sprintf("%s invited you to join %s on Nolyx Orbit as %s.\n\nAccept: %s\n\nThe link expires in 7 days.", currentUser(r).Email, orgName, in.Role, link)) == nil
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "invite.create", in.Email, map[string]any{"role": in.Role})
	writeJSON(w, 201, map[string]any{"link": link, "emailed": emailed})
	return nil
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var email string
	if err := s.db.QueryRow(r.Context(), `delete from invites where org_id::text = $1 and id::text = $2 returning email`, oid, r.PathValue("id")).Scan(&email); err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "invite.revoke", email, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) getInvite(w http.ResponseWriter, r *http.Request) error {
	out, err := s.one(r.Context(), `select o.name as org, i.email, i.role from invites i join organizations o on o.id = i.org_id
		where i.token_hash = $1 and i.accepted_at is null and i.expires_at > now()`, hashToken(r.PathValue("token")))
	if err == pgx.ErrNoRows {
		return errStatus(404, "this invitation is invalid or has expired")
	}
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// acceptInvite adds the signed-in user to the org. The invite email must match the account, so a
// leaked link can't be redeemed by someone else.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	u := currentUser(r)
	if u.Key != nil {
		return errStatus(403, "sign in to accept invitations")
	}
	var oid, role, email string
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `update invites set accepted_at = now() where token_hash = $1 and accepted_at is null and expires_at > now()
			returning org_id::text, role, email`, hashToken(in.Token)).Scan(&oid, &role, &email)
		if err == pgx.ErrNoRows {
			return errStatus(404, "this invitation is invalid or has expired")
		} else if err != nil {
			return err
		}
		if !strings.EqualFold(email, u.Email) {
			return errStatus(403, "this invitation was sent to %s; sign in with that account", email)
		}
		_, err = tx.Exec(r.Context(), `insert into memberships (org_id, user_id, role) values ($1,$2,$3) on conflict do nothing`, oid, u.ID, role)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(r, oid, "", u.ID, u.Email, "invite.accept", u.Email, map[string]any{"role": role})
	var pid string
	s.db.QueryRow(r.Context(), `select id::text from projects where org_id::text = $1 order by created_at limit 1`, oid).Scan(&pid)
	writeJSON(w, 200, map[string]string{"org_id": oid, "project_id": pid})
	return nil
}
