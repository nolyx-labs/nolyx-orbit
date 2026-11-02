// OS package updates: daily checks, holds and restarts. Rollouts are in rollouts.go.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"orbit/internal/protocol"
	"strings"
	"time"
)

// runUpdates is the worker's update and maintenance tick.
func (s *Server) runUpdates(ctx context.Context) {
	s.checkDueServers(ctx)
	s.claimRollouts(ctx)
	s.runMaintenance(ctx)
}

// checkDueServers checks each online server about once a day; failed checks are retried hourly.
func (s *Server) checkDueServers(ctx context.Context) {
	rows, err := s.db.Query(ctx, `insert into os_update_checks (server_id)
		select id from servers sv where status = 'online' and not exists (select 1 from os_update_checks c where c.server_id = sv.id
		  and c.attempted_at > now() - case when c.error = '' then interval '1 day' else interval '1 hour' end)
		on conflict (server_id) do update set attempted_at = now()
		returning server_id::text, (select project_id::text from servers where id = server_id)`)
	if err != nil {
		s.log.Error("claim update checks", "err", err)
		return
	}
	var due [][2]string
	for rows.Next() {
		var sid, pid string
		rows.Scan(&sid, &pid)
		due = append(due, [2]string{pid, sid})
	}
	rows.Close()
	s.checkServers(ctx, "", due)
}

// checkServers runs checks in the background, a few at a time.
func (s *Server) checkServers(ctx context.Context, userID string, servers [][2]string) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		sem := make(chan struct{}, 8)
		for _, x := range servers {
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				s.checkUpdates(ctx, x[0], x[1], userID)
			}()
		}
	}()
}

func (s *Server) checkUpdates(ctx context.Context, projectID, serverID, userID string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := s.sendCommand(ctx, newUUID(), serverID, userID, protocol.ActionOSCheck, struct{}{}, true)
	if err == nil && !res.OK {
		err = errors.New(res.Error)
	}
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not allowed") {
			msg = "This server's agent is too old to manage OS updates: update the agent"
		}
		s.db.Exec(ctx, `insert into os_update_checks (server_id, error) values ($1,$2)
			on conflict (server_id) do update set error = $2, attempted_at = now()`, serverID, trunc(msg, 500))
		s.hub.publish(projectID, "updates", serverID)
		return errStatus(502, "%s", msg)
	}
	var out protocol.OSCheckResult
	json.Unmarshal(res.Output, &out)
	s.storeCheck(ctx, serverID, out)
	s.hub.publish(projectID, "updates", serverID)
	return nil
}

func (s *Server) storeCheck(ctx context.Context, serverID string, c protocol.OSCheckResult) {
	if c.Packages == nil {
		c.Packages = []protocol.OSPackage{}
	}
	if c.Held == nil {
		c.Held = []string{}
	}
	_, err := s.db.Exec(ctx, `insert into os_update_checks (server_id, manager, packages, held, reboot_required, checked_at) values ($1,$2,$3,$4,$5,now())
		on conflict (server_id) do update set manager = $2, packages = $3, held = $4, reboot_required = $5, error = '', checked_at = now(), attempted_at = now()`,
		serverID, trunc(c.Manager, 16), mustJSON(c.Packages), mustJSON(c.Held), c.RebootRequired)
	if err != nil {
		s.log.Error("store update check", "server", serverID, "err", err)
	}
}

// listUpdates returns every server's check summary and the waiting updates grouped by package.
func (s *Server) listUpdates(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select
		(select coalesce(json_agg(x order by x.name), '[]') from (
		  select sv.id, sv.name, sv.status, sv.os, c.manager, coalesce(jsonb_array_length(c.packages), 0) waiting,
		    (select count(*) from jsonb_array_elements(c.packages) e where (e->>'security')::bool) security,
		    coalesce(c.reboot_required, false) reboot_required, coalesce(c.held, '[]') held, coalesce(c.error, '') error, c.checked_at, c.attempted_at
		  from servers sv left join os_update_checks c on c.server_id = sv.id where sv.project_id = $1) x) servers,
		(select coalesce(json_agg(x order by x.security desc, x.name), '[]') from (
		  select e->>'name' name, bool_or((e->>'security')::bool) security,
		    json_agg(json_build_object('id', sv.id, 'name', sv.name, 'installed', e->>'installed', 'available', e->>'available') order by sv.name) servers
		  from servers sv join os_update_checks c on c.server_id = sv.id, jsonb_array_elements(c.packages) e
		  where sv.project_id = $1 group by 1) x) packages`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) serverUpdates(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select sv.id, c.manager, coalesce(c.packages, '[]') packages, coalesce(c.held, '[]') held,
		coalesce(c.reboot_required, false) reboot_required, coalesce(c.error, '') error, c.checked_at, c.attempted_at
		from servers sv left join os_update_checks c on c.server_id = sv.id where sv.project_id = $1 and sv.id::text = $2`, p.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// serverInProject resolves {sid} and returns its name.
func (s *Server) serverInProject(r *http.Request, p project) (string, string, error) {
	var id, name string
	err := s.db.QueryRow(r.Context(), `select id::text, name from servers where project_id = $1 and id::text = $2`, p.ID, r.PathValue("sid")).Scan(&id, &name)
	if err != nil {
		return "", "", errStatus(404, "server not found")
	}
	return id, name, nil
}

func (s *Server) checkServerUpdates(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	sid, name, err := s.serverInProject(r, p)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "os_updates.check", name, nil)
	if err := s.checkUpdates(r.Context(), p.ID, sid, currentUser(r).ID); err != nil {
		return err
	}
	return s.serverUpdates(w, r)
}

func (s *Server) checkAllUpdates(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	rows, err := s.db.Query(r.Context(), `select id::text from servers where project_id = $1 and status = 'online'`, p.ID)
	if err != nil {
		return err
	}
	var due [][2]string
	for rows.Next() {
		var sid string
		rows.Scan(&sid)
		due = append(due, [2]string{p.ID, sid})
	}
	rows.Close()
	s.auditProject(r, p, "os_updates.check_all", "", map[string]any{"servers": len(due)})
	s.checkServers(r.Context(), currentUser(r).ID, due)
	writeJSON(w, 202, map[string]int{"servers": len(due)})
	return nil
}

// holdPackage holds a package at its version (or lets it go) on the given servers.
func (s *Server) holdPackage(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in struct {
		Package   string   `json:"package"`
		Hold      bool     `json:"hold"`
		ServerIDs []string `json:"server_ids"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !protocol.ValidPackage(in.Package) {
		return errStatus(400, "invalid package name")
	}
	if len(in.ServerIDs) == 0 || len(in.ServerIDs) > 1000 {
		return errStatus(400, "choose the servers")
	}
	rows, err := s.db.Query(r.Context(), `select id::text, name from servers where project_id = $1 and id::text = any($2)`, p.ID, in.ServerIDs)
	if err != nil {
		return err
	}
	var targets [][2]string
	for rows.Next() {
		var id, name string
		rows.Scan(&id, &name)
		targets = append(targets, [2]string{id, name})
	}
	rows.Close()
	if len(targets) != len(in.ServerIDs) {
		return errStatus(400, "server not found")
	}
	action := "os_updates.release"
	if in.Hold {
		action = "os_updates.hold"
	}
	s.auditProject(r, p, action, in.Package, map[string]any{"servers": in.ServerIDs})
	failed := map[string]string{}
	for _, t := range targets {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		res, err := s.sendCommand(ctx, newUUID(), t[0], currentUser(r).ID, protocol.ActionOSHold, protocol.OSHoldArgs{Package: in.Package, Hold: in.Hold}, true)
		cancel()
		if err == nil && !res.OK {
			err = errors.New(res.Error)
		}
		if err != nil {
			failed[t[1]] = err.Error()
			continue
		}
		var held []string
		json.Unmarshal(res.Output, &held)
		if held == nil {
			held = []string{}
		}
		s.db.Exec(r.Context(), `insert into os_update_checks (server_id, held) values ($1,$2) on conflict (server_id) do update set held = $2`, t[0], mustJSON(held))
	}
	s.hub.publish(p.ID, "updates", "")
	writeJSON(w, 200, map[string]any{"failed": failed})
	return nil
}

func (s *Server) rebootServer(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	sid, name, err := s.serverInProject(r, p)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "server.reboot", name, nil)
	if err := s.reboot(r.Context(), p.ID, sid, currentUser(r).ID); err != nil {
		return err
	}
	s.event(r.Context(), p.ID, sid, "warning", "Server "+name+" is restarting")
	w.WriteHeader(204)
	return nil
}

// reboot restarts a server, holding its alerts while it comes back.
func (s *Server) reboot(ctx context.Context, projectID, serverID, userID string) error {
	res, err := s.sendCommand(ctx, newUUID(), serverID, userID, protocol.ActionOSReboot, struct{}{}, true)
	if err == nil && !res.OK {
		err = errStatus(502, "%s", res.Error)
	}
	if err != nil {
		return err
	}
	s.db.Exec(ctx, `update servers set alerts_held_until = now() + interval '10 minutes' where id::text = $1`, serverID)
	s.hub.publish(projectID, "servers", serverID)
	return nil
}
