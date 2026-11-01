// Teams and their per-project role grants.
package server

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
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
		return errStatus(400, "name is required")
	}
	out, err := s.one(r.Context(), `insert into teams (org_id, name) values ($1,$2) on conflict do nothing returning id`, oid, in.Name)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a team with that name exists")
	} else if err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "team.create", in.Name, nil)
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from teams where org_id::text = $1 and id::text = $2 returning name`, oid, r.PathValue("tid")).Scan(&name); err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "team.delete", name, nil)
	w.WriteHeader(204)
	return nil
}

// putTeam replaces a team's members and project grants.
func (s *Server) putTeam(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		UserIDs []string `json:"user_ids"`
		Grants  []struct {
			ProjectID string `json:"project_id"`
			Role      string `json:"role"`
		} `json:"grants"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	tid := r.PathValue("tid")
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from teams where org_id::text = $1 and id::text = $2`, oid, tid).Scan(&name); err != nil {
		return err
	}
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		tx.Exec(r.Context(), `delete from team_members where team_id::text = $1`, tid)
		tx.Exec(r.Context(), `delete from team_project_roles where team_id::text = $1`, tid)
		for _, uid := range in.UserIDs {
			tag, err := tx.Exec(r.Context(), `insert into team_members select $1::uuid, user_id from memberships where org_id::text = $2 and user_id::text = $3`, tid, oid, uid)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errStatus(400, "user is not a member of this organization")
			}
		}
		for _, g := range in.Grants {
			if g.Role != "viewer" && g.Role != "member" && g.Role != "admin" {
				return errStatus(400, "invalid role")
			}
			tag, err := tx.Exec(r.Context(), `insert into team_project_roles select $1::uuid, id, $3 from projects where org_id::text = $2 and id::text = $4`, tid, oid, g.Role, g.ProjectID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errStatus(400, "project not found")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "team.update", name, map[string]any{"members": len(in.UserIDs), "grants": in.Grants})
	w.WriteHeader(204)
	return nil
}
