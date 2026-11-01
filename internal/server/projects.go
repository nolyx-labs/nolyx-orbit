// Projects: create, rename, delete.
package server

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.orgAccess(r, "admin"); err != nil {
		return err
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 64 {
		return errStatus(400, "name is required (max 64 chars)")
	}
	out, err := s.one(r.Context(), `insert into projects (org_id, name) values ($1,$2) on conflict do nothing returning id, name`, r.PathValue("oid"), body.Name)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a project with that name exists")
	} else if err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, r.PathValue("oid"), "", u.ID, u.label(), "project.create", body.Name, nil)
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" || len(in.Name) > 64 {
		return errStatus(400, "name is required (max 64 chars)")
	}
	if _, err := s.db.Exec(r.Context(), `update projects set name = $2 where id = $1`, p.ID, in.Name); err != nil {
		return errStatus(409, "a project with that name exists")
	}
	s.auditProject(r, p, "project.rename", in.Name, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	var servers, projects int
	s.db.QueryRow(r.Context(), `select name, (select count(*) from servers where project_id = $1), (select count(*) from projects where org_id = $2)
		from projects where id = $1`, p.ID, p.OrgID).Scan(&name, &servers, &projects)
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != name {
		return errStatus(400, "type the project name to confirm")
	}
	if servers > 0 {
		return errStatus(409, "remove the project's %d server(s) first", servers)
	}
	if projects <= 1 {
		return errStatus(409, "an organization needs at least one project")
	}
	s.auditProject(r, p, "project.delete", name, nil)
	s.db.Exec(r.Context(), `delete from projects where id = $1`, p.ID)
	w.WriteHeader(204)
	return nil
}
