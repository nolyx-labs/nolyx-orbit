// Organization settings.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

func (s *Server) getOrg(w http.ResponseWriter, r *http.Request) error {
	oid, role, err := s.orgAccess(r, "viewer")
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := map[string]json.RawMessage{}
	q := map[string]string{
		"members": `select u.id, u.email, u.name, m.role, m.custom_role_id, u.totp_enabled_at is not null as mfa_enabled,
			(select coalesce(json_agg(t.name order by t.name), '[]') from team_members tm join teams t on t.id = tm.team_id where tm.user_id = u.id and t.org_id = m.org_id) as teams
			from memberships m join users u on u.id = m.user_id where m.org_id::text = $1 order by u.email`,
		"invites": `select i.id, i.email, i.role, i.expires_at, i.created_at, u.email as invited_by from invites i left join users u on u.id = i.invited_by
			where i.org_id::text = $1 and i.accepted_at is null and i.expires_at > now() order by i.created_at desc`,
		"teams": `select t.id, t.name, t.require_mfa,
			(select coalesce(json_agg(json_build_object('id', u.id, 'email', u.email) order by u.email), '[]') from team_members tm join users u on u.id = tm.user_id where tm.team_id = t.id) as members,
			(select coalesce(json_agg(json_build_object('project_id', p.id, 'project', p.name, 'role', g.role) order by p.name), '[]') from team_project_roles g join projects p on p.id = g.project_id where g.team_id = t.id) as grants
			from teams t where t.org_id::text = $1 order by t.name`,
		"projects": `select p.id, p.name, p.created_at, (select count(*) from servers s where s.project_id = p.id) as servers,
			(select count(*) from applications a where a.project_id = p.id) as apps, (select count(*) from databases d where d.project_id = p.id) as databases
			from projects p where p.org_id::text = $1 order by p.created_at`,
	}
	for k, sql := range q {
		if out[k], err = s.list(ctx, sql, oid); err != nil {
			return err
		}
	}
	if out["org"], err = s.one(ctx, `select id, name, created_at, require_mfa, require_sso, audit_retention_days from organizations where id::text = $1`, oid); err != nil {
		return err
	}
	out["role"] = mustJSON(role)
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) updateOrg(w http.ResponseWriter, r *http.Request) error {
	oid, role, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Name       *string `json:"name"`
		RequireMFA *bool   `json:"require_mfa"`
		RequireSSO *bool   `json:"require_sso"`
		AuditDays  *int    `json:"audit_retention_days"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.RequireSSO != nil && role != "owner" {
		return errStatus(403, "only owners can change the single sign-on requirement")
	}
	if in.AuditDays != nil && role != "owner" {
		return errStatus(403, "only owners can change how long the audit log is kept")
	}
	if in.AuditDays != nil && !slices.Contains([]int{0, 90, 180, 365, 730}, *in.AuditDays) {
		return errStatus(400, "audit_retention_days must be 0 (forever), 90, 180, 365 or 730")
	}
	if in.RequireSSO != nil && *in.RequireSSO && s.oidc == nil {
		return errStatus(400, "single sign-on isn't set up on this instance (OIDC_ISSUER)")
	}
	u := currentUser(r)
	if in.Name != nil {
		if *in.Name = strings.TrimSpace(*in.Name); *in.Name == "" || len(*in.Name) > 80 {
			return errStatus(400, "name is required")
		}
		s.db.Exec(r.Context(), `update organizations set name = $2 where id::text = $1`, oid, *in.Name)
		s.audit(r, oid, "", u.ID, u.label(), "org.rename", *in.Name, nil)
	}
	if in.RequireMFA != nil {
		if *in.RequireMFA {
			if err := s.requireOwnMFA(r); err != nil {
				return err
			}
		}
		if _, err := s.db.Exec(r.Context(), `update organizations set require_mfa = $2 where id::text = $1`, oid, *in.RequireMFA); err != nil {
			return err
		}
		s.audit(r, oid, "", u.ID, u.label(), "org.require_mfa", fmt.Sprint(*in.RequireMFA), nil)
	}
	if in.RequireSSO != nil {
		if _, err := s.db.Exec(r.Context(), `update organizations set require_sso = $2 where id::text = $1`, oid, *in.RequireSSO); err != nil {
			return err
		}
		s.audit(r, oid, "", u.ID, u.label(), "org.require_sso", fmt.Sprint(*in.RequireSSO), nil)
	}
	if in.AuditDays != nil {
		if _, err := s.db.Exec(r.Context(), `update organizations set audit_retention_days = $2 where id::text = $1`, oid, *in.AuditDays); err != nil {
			return err
		}
		s.audit(r, oid, "", u.ID, u.label(), "org.audit_retention", fmt.Sprint(*in.AuditDays), nil)
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) deleteOrg(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "owner")
	if err != nil {
		return err
	}
	var name string
	s.db.QueryRow(r.Context(), `select name from organizations where id::text = $1`, oid).Scan(&name)
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != name {
		return errStatus(400, "type the organization name to confirm")
	}
	var servers int
	s.db.QueryRow(r.Context(), `select count(*) from servers s join projects p on p.id = s.project_id where p.org_id::text = $1`, oid).Scan(&servers)
	if servers > 0 {
		return errStatus(409, "remove the organization's %d server(s) first", servers)
	}
	if _, err := s.db.Exec(r.Context(), `delete from organizations where id::text = $1`, oid); err != nil {
		return err
	}
	s.log.Info("organization deleted", "org", oid, "by", currentUser(r).Email) // audit rows go with the org
	w.WriteHeader(204)
	return nil
}
