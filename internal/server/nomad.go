package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"orbit/internal/protocol"
)

func (s *Server) storeNomad(ctx context.Context, ac *agentConn, snap protocol.NomadSnapshot) error {
	peers := append([]string{}, snap.Peers...)
	sort.Strings(peers)
	key := snap.Region + "|" + strings.Join(peers, ",")
	nz := func(b json.RawMessage) json.RawMessage {
		if len(b) == 0 || string(b) == "null" {
			return json.RawMessage("[]")
		}
		return b
	}
	_, err := s.db.Exec(ctx, `
		insert into nomad_clusters (project_id, key, region, datacenter, leader, peers, nodes, jobs, allocations, deployments, reporters, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, array[$11::uuid], now())
		on conflict (project_id, key) do update set region = $3, datacenter = $4, leader = $5, peers = $6, nodes = $7, jobs = $8,
		  allocations = $9, deployments = $10, updated_at = now(),
		  reporters = (select array_agg(distinct x) from unnest(nomad_clusters.reporters || $11::uuid) x)`,
		ac.projectID, key, snap.Region, snap.Datacenter, snap.Leader, mustJSON(peers), nz(snap.Nodes), nz(snap.Jobs), nz(snap.Allocations), nz(snap.Deployments), ac.serverID)
	if err == nil {
		s.hub.publish(ac.projectID, "nomad", "")
	}
	return err
}

func (s *Server) listNomadClusters(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select id, region, datacenter, leader, peers, nodes, jobs, allocations, deployments, reporters, updated_at,
		updated_at > now() - interval '2 minutes' as fresh
		from nomad_clusters where project_id = $1 order by region, key`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// clusterAgent picks a connected agent that reports for the cluster, to send Nomad commands through.
func (s *Server) clusterAgent(ctx context.Context, p project, clusterID string) (string, error) {
	var reporters []string
	if err := s.db.QueryRow(ctx, `select reporters::text[] from nomad_clusters where project_id = $1 and id::text = $2`, p.ID, clusterID).Scan(&reporters); err != nil {
		return "", errStatus(400, "Nomad cluster not found")
	}
	for _, id := range reporters {
		if s.hub.connected(id) {
			return id, nil
		}
	}
	return "", errStatus(409, "no connected server in this Nomad cluster")
}

func (s *Server) createNomadCluster(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		ServerIDs  []string `json:"server_ids"`
		Servers    int      `json:"servers"` // how many of them run the Nomad server role: 1 or 3
		Datacenter string   `json:"datacenter"`
		Region     string   `json:"region"`
		Version    string   `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if len(in.ServerIDs) == 0 || (in.Servers != 1 && in.Servers != 3) || in.Servers > len(in.ServerIDs) {
		return errStatus(400, "choose at least one server; the server role runs on 1 or 3 of them")
	}
	if in.Datacenter == "" {
		in.Datacenter = "dc1"
	}
	if in.Region == "" {
		in.Region = "global"
	}
	if in.Version == "" {
		in.Version = "1.10"
	}
	type target struct{ id, name, ip string }
	var targets []target
	for _, id := range in.ServerIDs {
		var t target
		if err := s.db.QueryRow(r.Context(), `select id::text, name, ip from servers where project_id = $1 and id::text = $2`, p.ID, id).Scan(&t.id, &t.name, &t.ip); err != nil {
			return errStatus(400, "server not found in this project")
		}
		if !s.hub.connected(t.id) {
			return errStatus(409, "%s is offline", t.name)
		}
		if t.ip == "" {
			return errStatus(400, "%s has no known IP address", t.name)
		}
		targets = append(targets, t)
	}
	var join []string
	for _, t := range targets[:in.Servers] {
		join = append(join, t.ip)
	}
	s.auditProject(r, p, "nomad.cluster.create", in.Region+"/"+in.Datacenter, map[string]any{"servers": len(targets), "server_role": in.Servers})
	var ops []string
	for i, t := range targets {
		args := protocol.NomadInstallArgs{Server: i < in.Servers, BootstrapExpect: in.Servers, Join: join,
			Datacenter: in.Datacenter, Region: in.Region, Version: in.Version, AdvertiseIP: t.ip}
		id, err := s.startOperation(r, p, "nomad.install", t.name, t.id, "",
			[]string{"Pulling Nomad", "Creating Nomad agent", "Starting Nomad", "Waiting for Nomad API"}, protocol.ActionNomadInstall, args)
		if err != nil {
			return err
		}
		ops = append(ops, id)
	}
	writeJSON(w, 202, map[string]any{"operation_ids": ops})
	return nil
}

func (s *Server) stopNomadJob(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	job := r.PathValue("job")
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != job {
		return errStatus(400, "type the job name to confirm")
	}
	agent, err := s.clusterAgent(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	s.auditProject(r, p, "nomad.job.stop", job, nil)
	opID, err := s.startOperation(r, p, "nomad.stop", job, agent, "", nil, protocol.ActionNomadStop, protocol.NomadJobArgs{JobID: job})
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": opID})
	return nil
}

// nomadJob translates an Orbit service into a Nomad job: one instance per host (static ports),
// rolling updates with auto-revert, and Nomad-native health checks.
func nomadJob(name, image string, ports []string, env map[string]string, health string, instances int, cpus float64, memMB int) (json.RawMessage, error) {
	ns := func(d time.Duration) int64 { return int64(d) }
	var reserved []map[string]any
	var labels []string
	for i, p := range ports {
		host, ctr, err := protocol.ParsePort(p)
		if err != nil {
			return nil, err
		}
		label := fmt.Sprintf("p%d", i)
		var hp, cp int
		fmt.Sscan(host, &hp)
		fmt.Sscan(strings.Split(ctr, "/")[0], &cp)
		reserved = append(reserved, map[string]any{"Label": label, "Value": hp, "To": cp})
		labels = append(labels, label)
	}
	mhz := int(cpus * 1000)
	if mhz <= 0 {
		mhz = 100
	}
	if memMB <= 0 {
		memMB = 256
	}
	healthMode := "task_states"
	var services []map[string]any
	if len(labels) > 0 {
		svc := map[string]any{"Name": name, "PortLabel": labels[0], "Provider": "nomad"}
		if health != "" {
			healthMode = "checks"
			svc["Checks"] = []map[string]any{{"Name": "http", "Type": "http", "Path": health, "Interval": ns(10 * time.Second), "Timeout": ns(3 * time.Second)}}
		}
		services = append(services, svc)
	}
	job := map[string]any{
		"ID": name, "Name": name, "Type": "service", "Datacenters": []string{"*"},
		"Meta":   map[string]string{"orbit.app": name},
		"Spread": []map[string]any{{"Attribute": "${node.datacenter}", "Weight": 50}},
		"Update": map[string]any{"MaxParallel": 1, "HealthCheck": healthMode, "MinHealthyTime": ns(10 * time.Second),
			"HealthyDeadline": ns(5 * time.Minute), "ProgressDeadline": ns(10 * time.Minute), "AutoRevert": true},
		"TaskGroups": []map[string]any{{
			"Name": name, "Count": instances,
			"Constraints": []map[string]any{{"Operand": "distinct_hosts", "RTarget": "true"}},
			"Networks":    []map[string]any{{"ReservedPorts": reserved}},
			"Services":    services,
			"Tasks": []map[string]any{{
				"Name": name, "Driver": "docker",
				"Config":    map[string]any{"image": image, "ports": labels},
				"Env":       env,
				"Resources": map[string]any{"CPU": mhz, "MemoryMB": memMB},
			}},
		}},
	}
	return json.Marshal(job)
}
