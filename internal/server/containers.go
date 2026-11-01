// Containers: list, actions and logs.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `
		select c.docker_id as id, c.server_id, s.name as server_name, c.name, c.image, c.state, c.status, c.health, c.created,
		  c.ports, c.labels, c.restart_count, c.cpu, c.mem_used, c.mem_limit, c.updated_at
		from containers c join servers s on s.id = c.server_id
		where s.project_id = $1 and ($2 = '' or c.server_id::text = $2)
		order by s.name, c.name`, p.ID, r.URL.Query().Get("server"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// containerTarget verifies the container belongs to a server in the project before any command is sent.
func (s *Server) containerTarget(r *http.Request, p project) (name string, err error) {
	err = s.db.QueryRow(r.Context(), `select c.name from containers c join servers s on s.id = c.server_id
		where s.project_id = $1 and s.id::text = $2 and c.docker_id = $3`, p.ID, r.PathValue("sid"), r.PathValue("cid")).Scan(&name)
	if err == pgx.ErrNoRows {
		return "", errStatus(404, "container not found")
	}
	return name, err
}

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request) error {
	action := "container." + r.PathValue("action")
	minRole := map[string]string{
		protocol.ActionContainerStart: "member", protocol.ActionContainerStop: "member",
		protocol.ActionContainerRestart: "member", protocol.ActionContainerRemove: "admin",
	}[action]
	if minRole == "" {
		return errStatus(404, "unknown action")
	}
	p, err := s.projectAccess(r, minRole)
	if err != nil {
		return err
	}
	name, err := s.containerTarget(r, p)
	if err != nil {
		return err
	}
	if action == protocol.ActionContainerRemove {
		var body struct {
			Confirm string `json:"confirm"`
		}
		if err := decode(r, &body); err != nil || body.Confirm != name {
			return errStatus(400, "type the container name to confirm removal")
		}
	}
	s.auditProject(r, p, action, name, map[string]any{"server_id": r.PathValue("sid")})
	res, err := s.sendCommand(r.Context(), newUUID(), r.PathValue("sid"), currentUser(r).ID, action,
		protocol.ContainerArgs{ContainerID: r.PathValue("cid")}, true)
	if err != nil {
		return err
	}
	if !res.OK {
		return errStatus(502, "agent: %s", res.Error)
	}
	s.event(r.Context(), p.ID, r.PathValue("sid"), "info", fmt.Sprintf("%s: %s by %s", strings.TrimPrefix(action, "container."), name, currentUser(r).Email))
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	if _, err := s.containerTarget(r, p); err != nil {
		return err
	}
	tail := 500
	fmt.Sscan(r.URL.Query().Get("tail"), &tail)
	res, err := s.sendCommand(r.Context(), newUUID(), r.PathValue("sid"), currentUser(r).ID, protocol.ActionContainerLogs,
		protocol.ContainerArgs{ContainerID: r.PathValue("cid"), Tail: min(max(tail, 1), 5000)}, true)
	if err != nil {
		return err
	}
	if !res.OK {
		return errStatus(502, "agent: %s", res.Error)
	}
	var logs string
	json.Unmarshal(res.Output, &logs)
	writeJSON(w, 200, map[string]string{"logs": logs})
	return nil
}
