// Audit log and infrastructure event timeline.
package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// audit records a privileged operation. Failures are logged loudly but don't fail the request,
// because the operation already happened.
func (s *Server) audit(r *http.Request, orgID, projectID, actorID, actor, action, target string, meta map[string]any) {
	ip := clientIP(r)
	m, _ := json.Marshal(meta)
	if meta == nil {
		m = []byte("{}")
	}
	_, err := s.db.Exec(context.WithoutCancel(r.Context()), `
		insert into audit_events (org_id, project_id, actor_id, actor, action, target, metadata, ip)
		values ($1, nullif($2,'')::uuid, nullif($3,'')::uuid, $4, $5, $6, $7, $8)`,
		orgID, projectID, actorID, actor, action, target, m, ip)
	if err != nil {
		s.log.Error("AUDIT WRITE FAILED", "action", action, "target", target, "err", err)
	}
}

func (s *Server) auditProject(r *http.Request, p project, action, target string, meta map[string]any) {
	u := currentUser(r)
	s.audit(r, p.OrgID, p.ID, u.ID, u.label(), action, target, meta)
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.orgAccess(r, "admin"); err != nil {
		return err
	}
	out, err := s.list(r.Context(), `
		select a.id, a.actor, a.action, a.target, a.metadata, a.ip, a.created_at, p.name as project
		from audit_events a left join projects p on p.id = a.project_id
		where a.org_id::text = $1 order by a.id desc limit 200`, r.PathValue("oid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// auditExport streams an organization's whole audit log as CSV (default) or JSON Lines (?format=jsonl).
func (s *Server) auditExport(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	jsonl := r.URL.Query().Get("format") == "jsonl"
	rows, err := s.db.Query(r.Context(), `
		select a.id, a.created_at, a.actor, a.action, a.target, coalesce(p.name, ''), a.ip, a.metadata::text
		from audit_events a left join projects p on p.id = a.project_id
		where a.org_id::text = $1 order by a.id`, oid)
	if err != nil {
		return err
	}
	defer rows.Close()
	ext, ctype := "csv", "text/csv"
	if jsonl {
		ext, ctype = "jsonl", "application/x-ndjson"
	}
	s.audit(r, oid, "", currentUser(r).ID, currentUser(r).label(), "audit.export", ext, nil)
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="orbit-audit-%s.%s"`, time.Now().UTC().Format("2006-01-02"), ext))
	cw := csv.NewWriter(w)
	if !jsonl {
		cw.Write([]string{"id", "time", "actor", "action", "target", "project", "ip", "metadata"})
	}
	enc := json.NewEncoder(w)
	for rows.Next() {
		var id int64
		var at time.Time
		var actor, action, target, project, ip, meta string
		if err := rows.Scan(&id, &at, &actor, &action, &target, &project, &ip, &meta); err != nil {
			return err
		}
		if jsonl {
			enc.Encode(map[string]any{"id": id, "time": at.UTC(), "actor": actor, "action": action, "target": target,
				"project": project, "ip": ip, "metadata": json.RawMessage(meta)})
		} else {
			cw.Write([]string{fmt.Sprint(id), at.UTC().Format(time.RFC3339), actor, action, target, project, ip, meta})
		}
	}
	cw.Flush()
	return rows.Err()
}

// event appends to the project's infrastructure timeline.
func (s *Server) event(ctx context.Context, projectID, serverID, severity, msg string) {
	_, err := s.db.Exec(context.WithoutCancel(ctx), `insert into events (project_id, server_id, severity, message) values ($1, nullif($2,'')::uuid, $3, $4)`,
		projectID, serverID, severity, msg)
	if err != nil {
		s.log.Error("event write failed", "err", err)
	}
	s.hub.publish(projectID, "events", "")
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select e.id, e.severity, e.message, e.created_at, e.server_id, s.name as server_name
		from events e left join servers s on s.id = e.server_id
		where e.project_id = $1 and ($2 = '' or e.server_id::text = $2) order by e.id desc limit 300`, p.ID, r.URL.Query().Get("server"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		return errStatus(500, "streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsub := s.hub.subscribe(p.ID)
	defer unsub()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		fl.Flush()
	}
}
