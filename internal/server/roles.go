// Custom roles: per-area caps on a member's organization role.
package server

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

var roleAreas = []string{"servers", "services", "databases", "monitoring", "workflows", "secrets", "updates"}

// areaOf maps the first path segment after /projects/{pid}/ to its area. Anything else (overview, events,
// metrics, logs, operations, project settings) isn't capped.
var areaOf = map[string]string{
	"servers": "servers", "server-groups": "servers", "enrollment-tokens": "servers", "containers": "servers",
	"cloud": "servers", "server-backups": "servers", "nomad": "servers",
	"apps": "services", "deployments": "services", "releases": "services", "catalog": "services", "app-metrics": "services",
	"databases": "databases", "provisions": "databases", "replicas": "databases", "backups": "databases", "database-metrics": "databases",
	"monitors": "monitoring", "status-pages": "monitoring", "incidents": "monitoring", "alerts": "monitoring", "channels": "monitoring",
	"workflows": "workflows", "secrets": "secrets", "updates": "updates",
}

// routeArea is the area of an API path such as /api/v1/projects/{pid}/servers/{sid}/reboot, or "".
func routeArea(path string) string {
	parts := strings.Split(path, "/") // "", "api", "v1", "projects", pid, segment, id, action
	if len(parts) < 6 || parts[3] != "projects" {
		return ""
	}
	if parts[5] == "servers" && len(parts) > 7 && (parts[7] == "updates" || parts[7] == "reboot") {
		return "updates"
	}
	return areaOf[parts[5]]
}

// cappedRank applies a custom role's level for area to an organization role's rank; -1 means no access.
func cappedRank(role string, perms map[string]string, area string) int {
	rank := roleRank[role]
	if perms == nil || area == "" || role == "owner" {
		return rank
	}
	lvl, ok := roleRank[perms[area]]
	if !ok || perms[area] == "owner" {
		lvl = -1
	}
	return min(rank, lvl)
}

type roleInput struct {
	Name        string            `json:"name"`
	Permissions map[string]string `json:"permissions"`
}

func (in *roleInput) validate() error {
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" || len(in.Name) > 64 {
		return errStatus(400, "name is required")
	}
	out := map[string]string{}
	for _, a := range roleAreas {
		lvl := in.Permissions[a]
		if lvl == "" {
			lvl = "none"
		}
		if lvl != "none" && lvl != "viewer" && lvl != "member" && lvl != "admin" {
			return errStatus(400, "%s: level must be none, viewer, member or admin", a)
		}
		out[a] = lvl
	}
	for k := range in.Permissions {
		if _, ok := out[k]; !ok {
			return errStatus(400, "unknown area %q", k)
		}
	}
	in.Permissions = out
	return nil
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select r.id, r.name, r.permissions,
		(select count(*) from memberships m where m.custom_role_id = r.id) as members
		from org_roles r where r.org_id::text = $1 order by r.name`, oid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"areas": roleAreas, "roles": out})
	return nil
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in roleInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	out, err := s.one(r.Context(), `insert into org_roles (org_id, name, permissions) values ($1,$2,$3) on conflict do nothing returning id`,
		oid, in.Name, in.Permissions)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a role with that name exists")
	} else if err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "role.create", in.Name, map[string]any{"permissions": in.Permissions})
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in roleInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `update org_roles set name = $3, permissions = $4 where org_id::text = $1 and id::text = $2`,
		oid, r.PathValue("id"), in.Name, in.Permissions)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return errStatus(409, "a role with that name exists")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return errStatus(404, "role not found")
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "role.update", in.Name, map[string]any{"permissions": in.Permissions})
	w.WriteHeader(204)
	return nil
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from org_roles where org_id::text = $1 and id::text = $2 returning name`, oid, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "role.delete", name, nil)
	w.WriteHeader(204)
	return nil
}
