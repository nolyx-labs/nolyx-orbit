// Organization and project secrets, merged into service environments at deploy time.
package server

import (
	"context"
	"net/http"
	"regexp"
)

var secretKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `
		select x.id, x.key, case when x.project_id is null then 'organization' else 'project' end as scope, x.updated_at, u.email as updated_by
		from secrets x left join users u on u.id = x.created_by
		where x.org_id = $1 and (x.project_id is null or x.project_id = $2)
		union all
		select null, k, 'application:' || a.name, a.created_at, null from applications a, jsonb_array_elements_text(a.env_keys) k where a.project_id = $2
		order by 3, 2`, p.OrgID, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Scope string `json:"scope"` // organization | project
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	minRole := "member"
	if in.Scope == "organization" {
		minRole = "admin"
	} else if in.Scope != "project" {
		return errStatus(400, "scope must be organization or project")
	}
	p, err := s.projectAccess(r, minRole)
	if err != nil {
		return err
	}
	if !secretKeyRe.MatchString(in.Key) {
		return errStatus(400, "invalid secret name")
	}
	if len(in.Value) > 64<<10 {
		return errStatus(400, "secret value too large")
	}
	var projectID *string
	if in.Scope == "project" {
		projectID = &p.ID
	}
	_, err = s.db.Exec(r.Context(), `insert into secrets (org_id, project_id, key, value_encrypted, created_by) values ($1,$2,$3,$4,$5)
		on conflict (org_id, coalesce(project_id, '00000000-0000-0000-0000-000000000000'), key)
		do update set value_encrypted = $4, created_by = $5, updated_at = now()`,
		p.OrgID, projectID, in.Key, s.encrypt([]byte(in.Value)), currentUser(r).ID)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "secret.set", in.Scope+":"+in.Key, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var key string
	var org bool
	if err := s.db.QueryRow(r.Context(), `select key, project_id is null from secrets where id::text = $1 and org_id = $2 and (project_id is null or project_id = $3)`,
		r.PathValue("id"), p.OrgID, p.ID).Scan(&key, &org); err != nil {
		return err
	}
	if org && roleRank[p.Role] < roleRank["admin"] {
		return errStatus(403, "organization secrets require the admin role")
	}
	s.db.Exec(r.Context(), `delete from secrets where id::text = $1`, r.PathValue("id"))
	s.auditProject(r, p, "secret.delete", key, map[string]any{"organization": org})
	w.WriteHeader(204)
	return nil
}

// resolveEnv merges organization → project secrets → the application's own variables (last wins).
func (s *Server) resolveEnv(ctx context.Context, p project, appID string) (map[string]string, error) {
	env := map[string]string{}
	rows, err := s.db.Query(ctx, `select key, value_encrypted from secrets where org_id = $1 and (project_id is null or project_id = $2)
		order by project_id nulls first`, p.OrgID, p.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var enc []byte
		rows.Scan(&k, &enc)
		v, err := s.decrypt(enc)
		if err != nil {
			rows.Close()
			return nil, err
		}
		env[k] = string(v)
	}
	rows.Close()
	appEnv, err := s.loadEnv(ctx, appID)
	if err != nil {
		return nil, err
	}
	for k, v := range appEnv {
		env[k] = v
	}
	return env, nil
}
