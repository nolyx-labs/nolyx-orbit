// The agent WebSocket session and frame dispatch.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
)

func (s *Server) agentConnect(w http.ResponseWriter, r *http.Request) {
	serverID, secret, _ := strings.Cut(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ":")
	var projectID, name string
	var stored []byte
	err := s.db.QueryRow(r.Context(), `select project_id::text, name, agent_secret_hash from servers where id::text = $1`, serverID).
		Scan(&projectID, &name, &stored)
	if err != nil || subtle.ConstantTimeCompare(stored, hashToken(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(8 << 20)
	ac := &agentConn{ws: ws, serverID: serverID, projectID: projectID}

	s.hub.mu.Lock()
	if old := s.hub.agents[serverID]; old != nil {
		old.ws.Close(websocket.StatusPolicyViolation, "replaced by new connection")
	}
	s.hub.agents[serverID] = ac
	s.hub.mu.Unlock()

	ctx := context.WithoutCancel(r.Context())
	s.db.Exec(ctx, `update servers set status = 'online', last_seen_at = now() where id = $1`, serverID)
	s.event(ctx, projectID, serverID, "info", fmt.Sprintf("Server %s connected", name))
	s.hub.publish(projectID, "servers", serverID)
	if err := s.pushConfig(ctx, serverID); err != nil {
		s.log.Error("push agent config", "server", serverID, "err", err)
	}

	err = s.agentLoop(r.Context(), ac)

	s.hub.mu.Lock()
	current := s.hub.agents[serverID] == ac
	if current {
		delete(s.hub.agents, serverID)
	}
	s.hub.mu.Unlock()
	if current {
		s.db.Exec(ctx, `update servers set status = 'offline' where id = $1`, serverID)
		s.event(ctx, projectID, serverID, "warning", fmt.Sprintf("Server %s disconnected", name))
		s.hub.publish(projectID, "servers", serverID)
	}
	s.log.Info("agent disconnected", "server", serverID, "err", err)
}

func (s *Server) agentLoop(ctx context.Context, ac *agentConn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // keepalive: detects half-open connections
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, c := context.WithTimeout(ctx, 10*time.Second)
				err := ac.ws.Ping(pctx)
				c()
				if err != nil {
					ac.ws.CloseNow()
					return
				}
			}
		}
	}()
	for {
		_, data, err := ac.ws.Read(ctx)
		if err != nil {
			return err
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if err := s.handleFrame(ctx, ac, env); err != nil {
			s.log.Warn("agent frame failed", "server", ac.serverID, "type", env.Type, "err", err)
		}
	}
}

func (s *Server) handleFrame(ctx context.Context, ac *agentConn, env protocol.Envelope) error {
	if _, err := s.db.Exec(ctx, `update servers set last_seen_at = now(), status = 'online' where id = $1`, ac.serverID); err != nil {
		return err
	}
	switch env.Type {
	case protocol.TypeHello:
		var h protocol.Hello
		if err := json.Unmarshal(env.Payload, &h); err != nil {
			return err
		}
		_, err := s.db.Exec(ctx, `update servers set hostname=$2, ip=$3, os=$4, arch=$5, kernel=$6, cpu_count=$7, mem_total=$8, disk_total=$9,
			agent_version=$10, docker_version=$11, nomad_version=$12, region = case when $13 = '' then region else $13 end where id=$1`,
			ac.serverID, trunc(h.Hostname, 255), trunc(h.IP, 64), trunc(h.OS, 128), trunc(h.Arch, 32), trunc(h.Kernel, 128), h.CPUCount,
			int64(h.MemTotal), int64(h.DiskTotal), trunc(h.AgentVersion, 32), trunc(h.DockerVersion, 32), trunc(h.NomadVersion, 32), trunc(h.Region, 64))
		s.hub.publish(ac.projectID, "servers", ac.serverID)
		return err

	case protocol.TypeMetrics:
		var b protocol.MetricsBatch
		if err := json.Unmarshal(env.Payload, &b); err != nil {
			return err
		}
		now := time.Now()
		batch := &pgx.Batch{}
		for _, m := range b.Samples {
			if m.TS.After(now.Add(time.Minute)) || m.TS.Before(now.Add(-2*time.Hour)) {
				continue // clock skew or stale backlog
			}
			batch.Queue(`insert into server_metrics (server_id, ts, cpu, mem_used, mem_total, disk_used, disk_total, net_rx, net_tx, load1, uptime, io_read, io_write, io_util)
				values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) on conflict do nothing`,
				ac.serverID, m.TS, m.CPU, int64(m.MemUsed), int64(m.MemTotal), int64(m.DiskUsed), int64(m.DiskTotal),
				int64(m.NetRx), int64(m.NetTx), m.Load1, int64(m.Uptime), int64(m.IORead), int64(m.IOWrite), m.IOUtil)
		}
		if err := s.db.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		s.hub.publish(ac.projectID, "metrics", ac.serverID)
		return nil

	case protocol.TypeContainers:
		var snap protocol.ContainerSnapshot
		if err := json.Unmarshal(env.Payload, &snap); err != nil {
			return err
		}
		return s.storeContainers(ctx, ac, snap)

	case protocol.TypeProgress:
		var p protocol.Progress
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return err
		}
		// Scoped by server_id so an agent can only touch work it was asked to run.
		line := ""
		if p.Line != "" {
			line = time.Now().UTC().Format("15:04:05") + "  " + trunc(p.Line, 2000) + "\n"
		}
		for table, topic := range map[string]string{"deployments": "deployments", "operations": "operations"} {
			tag, err := s.db.Exec(ctx, `
				update `+table+` set log = right(log || $3, 200000),
				  steps = case when $4 = '' then steps else steps || jsonb_build_object($4::text, $5::text) end
				where id::text = $1 and server_id::text = $2 and status = 'running'`, p.ID, ac.serverID, line, p.Step, p.Status)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				s.hub.publish(ac.projectID, topic, p.ID)
			}
		}
		return nil

	case protocol.TypeResult:
		var res protocol.Result
		if err := json.Unmarshal(env.Payload, &res); err != nil {
			return err
		}
		status := "succeeded"
		if !res.OK {
			status = "failed"
		}
		tag, err := s.db.Exec(ctx, `update commands set status = $3, error = nullif($4,''), completed_at = now()
			where id::text = $1 and server_id::text = $2 and status = 'pending'`, res.ID, ac.serverID, status, res.Error)
		if err != nil || tag.RowsAffected() == 0 {
			return err // unknown or foreign command id: ignore
		}
		s.finishDeployment(ctx, ac, res, status)
		s.finishOperation(ctx, ac, res, status)
		s.hub.mu.Lock()
		ch := s.hub.pending[res.ID]
		s.hub.mu.Unlock()
		if ch != nil {
			ch <- res
		}
		return nil

	case protocol.TypeDBStats:
		var st protocol.DBStats
		if err := json.Unmarshal(env.Payload, &st); err != nil {
			return err
		}
		return s.storeDBStats(ctx, ac, st)

	case protocol.TypeLogs:
		var b protocol.LogBatch
		if err := json.Unmarshal(env.Payload, &b); err != nil {
			return err
		}
		return s.storeLogs(ctx, ac, b)

	case protocol.TypeAppMetrics:
		var b protocol.AppMetricsBatch
		if err := json.Unmarshal(env.Payload, &b); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for _, m := range b.Apps {
			batch.Queue(`insert into app_metrics select id, $3, $4, $5, $6, $7 from applications
				where project_id::text = $1 and server_id::text = $2 and name = $8 on conflict do nothing`,
				ac.projectID, ac.serverID, b.TS, m.Requests, m.Errors, m.AvgMs, m.P95Ms, m.App)
		}
		return s.db.SendBatch(ctx, batch).Close()

	case protocol.TypeNomad:
		var snap protocol.NomadSnapshot
		if err := json.Unmarshal(env.Payload, &snap); err != nil {
			return err
		}
		return s.storeNomad(ctx, ac, snap)

	case protocol.TypeInventory:
		var inv protocol.Inventory
		if err := json.Unmarshal(env.Payload, &inv); err != nil {
			return err
		}
		return s.storeInventory(ctx, ac.projectID, ac.serverID, inv)

	case protocol.TypeSecurity:
		var facts protocol.SecurityFacts
		if err := json.Unmarshal(env.Payload, &facts); err != nil {
			return err
		}
		return s.storeScan(ctx, ac.projectID, ac.serverID, facts)
	}
	return nil
}

func (s *Server) storeContainers(ctx context.Context, ac *agentConn, snap protocol.ContainerSnapshot) error {
	type prev struct {
		name, state string
		restarts    int
	}
	before := map[string]prev{}
	rows, err := s.db.Query(ctx, `select docker_id, name, state, restart_count from containers where server_id = $1`, ac.serverID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var p prev
		rows.Scan(&id, &p.name, &p.state, &p.restarts)
		before[id] = p
	}
	rows.Close()

	ids := make([]string, 0, len(snap.Containers))
	batch := &pgx.Batch{}
	var events [][2]string
	for _, c := range snap.Containers {
		ids = append(ids, c.ID)
		ports, _ := json.Marshal(c.Ports)
		labels, _ := json.Marshal(c.Labels)
		batch.Queue(`
			insert into containers (server_id, docker_id, name, image, state, status, health, created, ports, labels, restart_count, cpu, mem_used, mem_limit, updated_at)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14, now())
			on conflict (server_id, docker_id) do update set name=$3, image=$4, state=$5, status=$6, health=$7, ports=$9, labels=$10,
			  restart_count=$11, cpu=$12, mem_used=$13, mem_limit=$14, updated_at=now()`,
			ac.serverID, c.ID, c.Name, c.Image, c.State, c.Status, c.Health, c.Created, ports, labels, c.RestartCount, c.CPU, int64(c.MemUsed), int64(c.MemLimit))
		if c.State == "running" {
			batch.Queue(`insert into container_metrics values ($1,$2,$3,$4,$5) on conflict do nothing`, ac.serverID, c.ID, snap.TS, c.CPU, int64(c.MemUsed))
		}
		if p, ok := before[c.ID]; ok {
			switch {
			case c.RestartCount > p.restarts:
				events = append(events, [2]string{"warning", fmt.Sprintf("Container %s restarted (restart count %d)", c.Name, c.RestartCount)})
				s.containerRestarted(ctx, ac, c.ID, c.Labels["orbit.app"], c.RestartCount-p.restarts)
			case p.state == "running" && c.State != "running":
				events = append(events, [2]string{"warning", fmt.Sprintf("Container %s is %s", c.Name, c.State)})
				s.containerStopped(ctx, ac, c.ID, c.Labels["orbit.app"], c.State)
			case p.state != "running" && c.State == "running":
				events = append(events, [2]string{"info", fmt.Sprintf("Container %s started", c.Name)})
			}
		}
	}
	batch.Queue(`delete from containers where server_id = $1 and not (docker_id = any($2))`, ac.serverID, ids)
	if err := s.db.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	for _, e := range events {
		s.event(ctx, ac.projectID, ac.serverID, e[0], e[1])
	}
	s.hub.publish(ac.projectID, "containers", ac.serverID)
	return nil
}
