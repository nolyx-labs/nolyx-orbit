// Servers: list, detail, edit, remove.
package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

const serverCols = `s.id, s.name, s.hostname, s.ip, s.os, s.arch, s.kernel, s.cpu_count, s.mem_total, s.disk_total,
	s.agent_version, s.docker_version, s.nomad_version, s.region, s.provider, s.status, s.last_seen_at, s.created_at,
	m.cpu, m.mem_used, m.disk_used, m.net_rx, m.net_tx, m.load1, m.uptime, m.io_read, m.io_write, m.io_util,
	(select count(*) from containers c where c.server_id = s.id and c.state = 'running') as containers_running`

const serverFrom = ` from servers s left join lateral (select * from server_metrics where server_id = s.id order by ts desc limit 1) m on true`

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select `+serverCols+serverFrom+` where s.project_id = $1 order by s.name`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select `+serverCols+serverFrom+` where s.project_id = $1 and s.id::text = $2`, p.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Name   *string `json:"name"`
		Region *string `json:"region"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 64) {
		return errStatus(400, "invalid name")
	}
	if in.Region != nil && len(*in.Region) > 64 {
		return errStatus(400, "invalid region")
	}
	tag, err := s.db.Exec(r.Context(), `update servers set name = coalesce($3, name), region = coalesce($4, region) where project_id = $1 and id::text = $2`,
		p.ID, r.PathValue("sid"), in.Name, in.Region)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.auditProject(r, p, "server.update", r.PathValue("sid"), map[string]any{"name": in.Name, "region": in.Region})
	s.hub.publish(p.ID, "servers", r.PathValue("sid"))
	return s.getServer(w, r)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from servers where project_id = $1 and id::text = $2 returning name`, p.ID, r.PathValue("sid")).Scan(&name); err != nil {
		return err
	}
	// Deleting the row revokes the agent secret; drop the live connection too.
	s.hub.mu.Lock()
	if ac := s.hub.agents[r.PathValue("sid")]; ac != nil {
		ac.ws.CloseNow()
	}
	s.hub.mu.Unlock()
	s.auditProject(r, p, "server.remove", name, nil)
	s.hub.publish(p.ID, "servers", "")
	w.WriteHeader(204)
	return nil
}

func (s *Server) checkServer(ctx context.Context, p project, serverID string) error {
	var ok bool
	s.db.QueryRow(ctx, `select exists(select 1 from servers where project_id = $1 and id::text = $2)`, p.ID, serverID).Scan(&ok)
	if !ok {
		return errStatus(400, "server not found in this project")
	}
	return nil
}
