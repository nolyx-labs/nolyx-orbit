// Server groups, group membership and clearing a server's collected data.
package server

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

var environments = map[string]bool{"production": true, "staging": true, "development": true}

func (s *Server) listServerGroups(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select g.id, g.name, (select count(*) from server_group_members m where m.group_id = g.id) as servers
		from server_groups g where g.project_id = $1 order by g.name`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createServerGroup(w http.ResponseWriter, r *http.Request) error {
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
		return errStatus(400, "give the group a name of up to 64 characters")
	}
	out, err := s.one(r.Context(), `insert into server_groups (project_id, name) values ($1, $2) on conflict do nothing returning id, name, 0 as servers`, p.ID, in.Name)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a group named %s already exists", in.Name)
	} else if err != nil {
		return err
	}
	s.auditProject(r, p, "server_group.create", in.Name, nil)
	s.hub.publish(p.ID, "servers", "")
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) deleteServerGroup(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from server_groups where project_id = $1 and id::text = $2 returning name`,
		p.ID, r.PathValue("gid")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "server_group.delete", name, nil)
	s.hub.publish(p.ID, "servers", "")
	w.WriteHeader(204)
	return nil
}

func (s *Server) putServerGroups(w http.ResponseWriter, r *http.Request) error {
	p, name, err := s.insightServer(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	sid := r.PathValue("sid")
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `delete from server_group_members where server_id::text = $1`, sid); err != nil {
			return err
		}
		// Only this project's groups: ids from another project are silently ignored.
		_, err := tx.Exec(r.Context(), `insert into server_group_members (group_id, server_id)
			select id, $2::uuid from server_groups where project_id = $1 and id::text = any($3)`, p.ID, sid, in.GroupIDs)
		return err
	})
	if err != nil {
		return err
	}
	s.auditProject(r, p, "server.groups", name, map[string]any{"group_ids": in.GroupIDs})
	s.hub.publish(p.ID, "servers", sid)
	return s.getServer(w, r)
}

// clearServerData deletes collected history but keeps the server connected.
func (s *Server) clearServerData(w http.ResponseWriter, r *http.Request) error {
	p, name, err := s.insightServer(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &in); err != nil || in.Confirm != name {
		return errStatus(400, "type the server name to confirm")
	}
	sid := r.PathValue("sid")
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		for _, table := range []string{"server_metrics", "container_metrics", "log_lines", "events"} {
			if _, err := tx.Exec(r.Context(), `delete from `+table+` where server_id::text = $1`, sid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.auditProject(r, p, "server.clear_data", name, nil)
	for _, topic := range []string{"metrics", "events", "servers"} {
		s.hub.publish(p.ID, topic, sid)
	}
	w.WriteHeader(204)
	return nil
}
