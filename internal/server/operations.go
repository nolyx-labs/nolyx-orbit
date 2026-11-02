// Long-running agent operations (provisioning, backups, promotions, Nomad installs).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
)

func (s *Server) startOperation(r *http.Request, p project, kind, target, serverID, dbID string, plan []string, action string, args any) (string, error) {
	id := newUUID()
	return id, s.startOperationID(r.Context(), r, p, id, kind, target, serverID, dbID, plan, action, args)
}

func (s *Server) startOperationID(ctx context.Context, r *http.Request, p project, id, kind, target, serverID, dbID string, plan []string, action string, args any) error {
	userID := ""
	if r != nil {
		userID = currentUser(r).ID
	}
	if plan == nil {
		plan = []string{}
	}
	if _, err := s.db.Exec(ctx, `insert into operations (id, project_id, server_id, database_id, kind, target, plan, triggered_by)
		values ($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,nullif($8,'')::uuid)`, id, p.ID, serverID, dbID, kind, target, mustJSON(plan), userID); err != nil {
		return err
	}
	if _, err := s.sendCommand(context.WithoutCancel(ctx), id, serverID, userID, action, args, false); err != nil {
		s.db.Exec(context.WithoutCancel(ctx), `update operations set status = 'failed', error = $2, finished_at = now() where id = $1`, id, err.Error())
		s.db.Exec(context.WithoutCancel(ctx), `update backups set status = 'failed', finished_at = now() where id = $1`, id)
		return err
	}
	s.hub.publish(p.ID, "operations", id)
	return nil
}

func (s *Server) finishOperation(ctx context.Context, ac *agentConn, res protocol.Result, status string) {
	var kind, target string
	var dbID *string
	output := res.Output
	if output == nil {
		output = []byte("null")
	}
	err := s.db.QueryRow(ctx, `update operations set status = $3, error = nullif($4,''), output = $5, finished_at = now()
		where id::text = $1 and server_id::text = $2 and status = 'running' returning kind, target, database_id::text`,
		res.ID, ac.serverID, status, res.Error, output).Scan(&kind, &target, &dbID)
	if err != nil {
		return // not an operation
	}
	ok := res.OK
	switch kind {
	case "db.provision", "db.replica.create", "db.resync":
		if ok {
			s.db.Exec(ctx, `update databases set status = 'unknown', error = '' where id::text = $1`, dbID)
		} else {
			s.db.Exec(ctx, `update databases set status = 'failed', error = $2 where id::text = $1`, dbID, res.Error)
		}
		s.pushConfig(ctx, ac.serverID)
	case "db.remove":
		if ok {
			s.db.Exec(ctx, `delete from databases where id::text = $1`, dbID)
			s.pushConfig(ctx, ac.serverID)
		}
	case "db.promote":
		if ok {
			s.db.Exec(ctx, `update databases set primary_id = null, role = 'primary' where id::text = $1`, dbID)
			s.event(ctx, ac.projectID, ac.serverID, "warning", fmt.Sprintf("Replica %s promoted to primary. The old primary still accepts writes: point applications at %s and stop or re-sync the old primary.", target, target))
		}
	case "db.backup":
		var out struct {
			Size     int64  `json:"size"`
			Location string `json:"location"`
		}
		json.Unmarshal(res.Output, &out)
		s.db.Exec(ctx, `update backups set status = $2, size = $3, location = $4, finished_at = now() where id::text = $1`, res.ID, status, out.Size, out.Location)
	}
	sev := "info"
	if !ok {
		sev = "error"
	}
	label := map[string]string{
		"db.provision": "Provisioning", "db.replica.create": "Replica creation", "db.resync": "Re-sync", "db.remove": "Removal",
		"db.promote": "Promotion", "db.backup": "Backup", "db.restore": "Restore", "nomad.install": "Nomad install", "nomad.stop": "Nomad job stop",
		"os.update": "OS update",
	}[kind]
	if label == "" {
		label = kind
	}
	msg := fmt.Sprintf("%s of %s %s", label, target, status)
	if !ok {
		msg += ": " + res.Error
	}
	s.event(ctx, ac.projectID, ac.serverID, sev, msg)
	s.hub.publish(ac.projectID, "operations", res.ID)
	s.hub.publish(ac.projectID, "databases", "")
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select o.id, o.kind, o.target, o.status, o.plan, o.steps, o.log, o.error, o.output, o.created_at, o.finished_at,
		o.database_id, o.server_id, s.name as server_name, u.email as triggered_by,
		extract(epoch from coalesce(o.finished_at, now()) - o.created_at)::int as duration
		from operations o left join servers s on s.id = o.server_id left join users u on u.id = o.triggered_by
		where o.project_id = $1 and o.id::text = $2`, p.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select o.id, o.kind, o.target, o.status, o.error, o.created_at, o.finished_at, s.name as server_name
		from operations o left join servers s on s.id = o.server_id
		where o.project_id = $1 and ($2 = '' or o.database_id::text = $2) order by o.created_at desc limit 50`, p.ID, r.URL.Query().Get("database"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
