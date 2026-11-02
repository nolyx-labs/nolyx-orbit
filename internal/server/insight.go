// Server insight: services, compose projects, ports, specs, security scan, firewall, disk usage and host actions.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
	"slices"
	"strings"
)

// scanResult is what's stored: the agent's raw facts plus the findings derived here.
type scanResult struct {
	protocol.SecurityFacts
	Findings []finding `json:"findings"`
}

func (s *Server) storeInventory(ctx context.Context, projectID, serverID string, inv protocol.Inventory) error {
	if inv.Services == nil {
		inv.Services = []protocol.SystemdService{} // queried with jsonb_array_elements, which rejects null
	}
	var name string
	var prev *protocol.Inventory
	if err := s.db.QueryRow(ctx, `select s.name, i.inventory from servers s left join server_insight i on i.server_id = s.id where s.id = $1`,
		serverID).Scan(&name, &prev); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `insert into server_insight (server_id, inventory, inventory_at) values ($1, $2, now())
		on conflict (server_id) do update set inventory = $2, inventory_at = now()`, serverID, mustJSON(inv)); err != nil {
		return err
	}
	if prev != nil {
		for _, u := range newlyFailed(prev.Services, inv.Services) {
			s.event(ctx, projectID, serverID, "warning", fmt.Sprintf("Service %s failed on %s", u, name))
		}
	}
	s.hub.publish(projectID, "insight", serverID)
	return nil
}

func newlyFailed(before, after []protocol.SystemdService) []string {
	was := map[string]bool{}
	for _, s := range before {
		was[s.Unit] = s.Active == "failed"
	}
	var out []string
	for _, s := range after {
		if s.Active == "failed" && !was[s.Unit] {
			out = append(out, s.Unit)
		}
	}
	return out
}

func (s *Server) storeScan(ctx context.Context, projectID, serverID string, facts protocol.SecurityFacts) error {
	res := scanResult{facts, securityFindings(facts)}
	var name string
	var prev *scanResult
	if err := s.db.QueryRow(ctx, `select s.name, i.scan from servers s left join server_insight i on i.server_id = s.id where s.id = $1`,
		serverID).Scan(&name, &prev); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `insert into server_insight (server_id, scan, scan_at) values ($1, $2, now())
		on conflict (server_id) do update set scan = $2, scan_at = now()`, serverID, mustJSON(res)); err != nil {
		return err
	}
	known := map[string]bool{}
	if prev != nil {
		for _, f := range prev.Findings {
			known[f.Key] = true
		}
	}
	for _, f := range res.Findings {
		if f.Severity == "high" && !known[f.Key] {
			s.event(ctx, projectID, serverID, "warning", fmt.Sprintf("Security scan on %s: %s", name, f.Title))
		}
	}
	s.hub.publish(projectID, "insight", serverID)
	return nil
}

// insightServer authorizes and resolves {sid} within the project.
func (s *Server) insightServer(r *http.Request, minRole string) (project, string, error) {
	p, err := s.projectAccess(r, minRole)
	if err != nil {
		return p, "", err
	}
	var name string
	err = s.db.QueryRow(r.Context(), `select name from servers where project_id = $1 and id::text = $2`, p.ID, r.PathValue("sid")).Scan(&name)
	return p, name, err
}

// hostCommand sends a host.* command and decodes its output. Callers authorize and audit first.
func (s *Server) hostCommand(r *http.Request, action string, args, out any) error {
	res, err := s.sendCommand(r.Context(), newUUID(), r.PathValue("sid"), currentUser(r).ID, action, args, true)
	if err != nil {
		return err
	}
	if !res.OK {
		if strings.Contains(res.Error, "not allowed") {
			return errStatus(502, "the agent on this server is too old for this: update it by re-running the install command")
		}
		return errStatus(502, "agent: %s", res.Error)
	}
	if out != nil {
		return json.Unmarshal(res.Output, out)
	}
	return nil
}

func (s *Server) getInsight(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select i.inventory, i.inventory_at, i.scan, i.scan_at, s.log_units
		from servers s left join server_insight i on i.server_id = s.id where s.project_id = $1 and s.id::text = $2`, p.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) refreshInsight(w http.ResponseWriter, r *http.Request) error {
	p, _, err := s.insightServer(r, "viewer")
	if err != nil {
		return err
	}
	var inv protocol.Inventory
	if err := s.hostCommand(r, protocol.ActionHostInventory, nil, &inv); err != nil {
		return err
	}
	if err := s.storeInventory(r.Context(), p.ID, r.PathValue("sid"), inv); err != nil {
		return err
	}
	return s.getInsight(w, r)
}

func (s *Server) scanServer(w http.ResponseWriter, r *http.Request) error {
	p, _, err := s.insightServer(r, "member")
	if err != nil {
		return err
	}
	var facts protocol.SecurityFacts
	if err := s.hostCommand(r, protocol.ActionHostScan, nil, &facts); err != nil {
		return err
	}
	if err := s.storeScan(r.Context(), p.ID, r.PathValue("sid"), facts); err != nil {
		return err
	}
	return s.getInsight(w, r)
}

func (s *Server) serviceAction(w http.ResponseWriter, r *http.Request) error {
	p, name, err := s.insightServer(r, "member")
	if err != nil {
		return err
	}
	var in protocol.ServiceArgs
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.Validate(); err != nil {
		return errStatus(400, "%v", err)
	}
	var known bool
	s.db.QueryRow(r.Context(), `select exists(select 1 from server_insight i, jsonb_array_elements(i.inventory->'services') x
		where i.server_id::text = $1 and x->>'unit' = $2)`, r.PathValue("sid"), in.Unit).Scan(&known)
	if !known {
		return errStatus(404, "%s isn't in this server's service list; refresh it and try again", in.Unit)
	}
	s.auditProject(r, p, "server.service."+in.Action, name+": "+in.Unit, map[string]any{"server_id": r.PathValue("sid")})
	if err := s.hostCommand(r, protocol.ActionHostService, in, nil); err != nil {
		return err
	}
	past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted", "reload": "reloaded"}[in.Action]
	severity := "info"
	if in.Action == "stop" {
		severity = "warning"
	}
	s.event(r.Context(), p.ID, r.PathValue("sid"), severity, fmt.Sprintf("Service %s %s on %s by %s", in.Unit, past, name, currentUser(r).Email))
	var inv protocol.Inventory
	if s.hostCommand(r, protocol.ActionHostInventory, nil, &inv) == nil {
		s.storeInventory(r.Context(), p.ID, r.PathValue("sid"), inv)
	}
	return s.getInsight(w, r)
}

func (s *Server) firewallAction(w http.ResponseWriter, r *http.Request) error {
	p, name, err := s.insightServer(r, "admin")
	if err != nil {
		return err
	}
	var in protocol.FirewallArgs
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.Validate(); err != nil {
		return errStatus(400, "%v", err)
	}
	from := "anywhere"
	if in.Source != "" {
		from = in.Source
	}
	rule := fmt.Sprintf("%d/%s from %s", in.Port, in.Proto, from)
	s.auditProject(r, p, "server.firewall."+in.Action, name+": "+rule, map[string]any{"server_id": r.PathValue("sid")})
	var facts protocol.SecurityFacts
	if err := s.hostCommand(r, protocol.ActionHostFirewall, in, &facts); err != nil {
		return err
	}
	s.event(r.Context(), p.ID, r.PathValue("sid"), "info", fmt.Sprintf("Firewall on %s: %s %s by %s",
		name, map[string]string{"open": "opened", "close": "closed"}[in.Action], rule, currentUser(r).Email))
	if err := s.storeScan(r.Context(), p.ID, r.PathValue("sid"), facts); err != nil {
		return err
	}
	return s.getInsight(w, r)
}

// diskUsage needs member: du walks a whole filesystem and costs real I/O on the server.
func (s *Server) diskUsage(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.insightServer(r, "member"); err != nil {
		return err
	}
	path := r.URL.Query().Get("path")
	if !strings.HasPrefix(path, "/") || len(path) > 1024 || strings.ContainsAny(path, "\x00\n") {
		return errStatus(400, "choose a filesystem")
	}
	var out protocol.DiskUsage
	if err := s.hostCommand(r, protocol.ActionHostDisk, protocol.DiskArgs{Path: path}, &out); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) dockerPrune(w http.ResponseWriter, r *http.Request) error {
	p, name, err := s.insightServer(r, "member")
	if err != nil {
		return err
	}
	s.auditProject(r, p, "server.docker.prune", name, map[string]any{"server_id": r.PathValue("sid")})
	var out protocol.DockerPruneResult
	if err := s.hostCommand(r, protocol.ActionHostDockerPrune, nil, &out); err != nil {
		return err
	}
	s.event(r.Context(), p.ID, r.PathValue("sid"), "info", fmt.Sprintf("Docker cleanup on %s freed %.1f GB (by %s)",
		name, float64(out.Reclaimed)/1e9, currentUser(r).Email))
	writeJSON(w, 200, out)
	return nil
}

// setLogUnits chooses the systemd services whose journal is collected with the container logs. Admin: service
// logs can hold more than an app's (auth attempts, other users' commands), even after redaction.
func (s *Server) setLogUnits(w http.ResponseWriter, r *http.Request) error {
	p, name, err := s.insightServer(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Units []string `json:"units"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if len(in.Units) > 50 {
		return errStatus(400, "collect logs from at most 50 services per server")
	}
	units := []string{}
	for _, u := range in.Units {
		if !protocol.ValidUnit(u) {
			return errStatus(400, "invalid service name %q", u)
		}
		if !slices.Contains(units, u) {
			units = append(units, u)
		}
	}
	if _, err := s.db.Exec(r.Context(), `update servers set log_units = $2 where id::text = $1`, r.PathValue("sid"), units); err != nil {
		return err
	}
	s.auditProject(r, p, "server.log_units", name, map[string]any{"units": units})
	s.pushConfig(r.Context(), r.PathValue("sid"))
	return s.getInsight(w, r)
}
