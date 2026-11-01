// Organization API keys.
package server

import (
	"net/http"
	"strings"
	"time"
)

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select k.id, k.name, k.prefix, k.role, k.project_id, p.name as project, k.last_used_at, k.expires_at, k.created_at, u.email as created_by
		from api_keys k left join projects p on p.id = k.project_id left join users u on u.id = k.created_by
		where k.org_id::text = $1 and k.revoked_at is null order by k.created_at desc`, oid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	oid, role, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	if currentUser(r).Key != nil {
		return errStatus(403, "API keys cannot create API keys")
	}
	var in struct {
		Name        string `json:"name"`
		Role        string `json:"role"`
		ProjectID   string `json:"project_id"`
		ExpiresDays int    `json:"expires_days"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return errStatus(400, "name is required")
	}
	if _, ok := roleRank[in.Role]; !ok || in.Role == "owner" || roleRank[in.Role] > roleRank[role] {
		return errStatus(400, "role must be viewer, member or admin (and not above your own)")
	}
	if in.ProjectID != "" {
		var ok bool
		s.db.QueryRow(r.Context(), `select exists(select 1 from projects where id::text = $1 and org_id::text = $2)`, in.ProjectID, oid).Scan(&ok)
		if !ok {
			return errStatus(400, "project not found")
		}
	}
	var expires *time.Time
	if in.ExpiresDays > 0 {
		t := time.Now().Add(time.Duration(in.ExpiresDays) * 24 * time.Hour)
		expires = &t
	}
	token, hash := newToken("orb_key_")
	var id string
	if err := s.db.QueryRow(r.Context(), `insert into api_keys (org_id, project_id, name, key_hash, prefix, role, created_by, expires_at)
		values ($1, nullif($2,'')::uuid, $3, $4, $5, $6, $7, $8) returning id::text`,
		oid, in.ProjectID, in.Name, hash, token[:12], in.Role, currentUser(r).ID, expires).Scan(&id); err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, in.ProjectID, u.ID, u.label(), "api_key.create", in.Name, map[string]any{"role": in.Role})
	writeJSON(w, 201, map[string]string{"id": id, "token": token})
	return nil
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `update api_keys set revoked_at = now() where org_id::text = $1 and id::text = $2 and revoked_at is null returning name`,
		oid, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "api_key.revoke", name, nil)
	w.WriteHeader(204)
	return nil
}
