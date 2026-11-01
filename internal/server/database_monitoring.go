// Database monitoring: agent config push and stats ingest.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"orbit/internal/protocol"
	"strings"

	"github.com/jackc/pgx/v5"
)

// pushConfig sends the agent the databases it should monitor (with credentials, over the authenticated session).
func (s *Server) pushConfig(ctx context.Context, serverID string) error {
	rows, err := s.db.Query(ctx, `select id::text, host, port, username, password_encrypted, dbname, sslmode
		from databases where server_id::text = $1 and status not in ('provisioning','failed')`, serverID)
	if err != nil {
		return err
	}
	cfg := protocol.AgentConfig{Databases: []protocol.DBConfig{}}
	for rows.Next() {
		var c protocol.DBConfig
		var enc []byte
		if err := rows.Scan(&c.ID, &c.Host, &c.Port, &c.User, &enc, &c.DBName, &c.SSLMode); err != nil {
			rows.Close()
			return err
		}
		pw, err := s.decrypt(enc)
		if err != nil {
			rows.Close()
			return err
		}
		c.Password = string(pw)
		cfg.Databases = append(cfg.Databases, c)
	}
	rows.Close()
	return s.sendFrame(ctx, serverID, protocol.TypeConfig, cfg)
}

// dbIssues explains why a database isn't healthy. The UI shows these verbatim.
func dbIssues(st protocol.DBStats) []string {
	issues := []string{}
	if !st.OK {
		return append(issues, "Connection failed: "+st.Error)
	}
	if st.MaxConnections > 0 && st.Connections*100 >= st.MaxConnections*90 {
		issues = append(issues, fmt.Sprintf("Connections at %d of %d", st.Connections, st.MaxConnections))
	}
	if st.Role == "replica" {
		if st.LagMs != nil && *st.LagMs > 1000 {
			issues = append(issues, fmt.Sprintf("Replication lag %.1fs", *st.LagMs/1000))
		}
		if st.Receiver == nil || st.Receiver.Status != "streaming" {
			status := "not running"
			if st.Receiver != nil {
				status = st.Receiver.Status
			}
			issues = append(issues, "WAL receiver "+status)
		}
	}
	for _, r := range st.Replicas {
		if r.State != "streaming" {
			issues = append(issues, fmt.Sprintf("Replica %s is %s", r.Name, r.State))
		}
	}
	if st.WaitingLocks > 10 {
		issues = append(issues, fmt.Sprintf("%d queries waiting on locks", st.WaitingLocks))
	}
	return issues
}

func (s *Server) storeDBStats(ctx context.Context, ac *agentConn, st protocol.DBStats) error {
	issues := dbIssues(st)
	status := "healthy"
	switch {
	case !st.OK:
		status = "down"
	case len(issues) > 0:
		status = "degraded"
	}
	stats, _ := json.Marshal(map[string]any{
		"connections": st.Connections, "max_connections": st.MaxConnections, "tps": st.TPS, "qps": st.QPS, "cache_hit": st.CacheHit,
		"size": st.Size, "waiting_locks": st.WaitingLocks, "slow_queries": st.SlowQueries, "tables": st.Tables, "wal_position": st.WALPosition,
		"lag_ms": st.LagMs, "replay_rate": st.ReplayRate, "receiver": st.Receiver, "replicas": st.Replicas, "issues": issues,
	})
	var old, name string
	err := s.db.QueryRow(ctx, `
		with old as (select status from databases where id::text = $1 and server_id::text = $2)
		update databases set status = $3, error = $4, stats = $5, last_seen_at = now(),
		  role = case when $6 = '' then role else $6 end, version = case when $7 = '' then version else $7 end,
		  system_id = case when $8 = '' then system_id else $8 end
		where id::text = $1 and server_id::text = $2 and status not in ('provisioning','failed')
		returning (select status from old), name`,
		st.ID, ac.serverID, status, st.Error, stats, st.Role, trunc(st.Version, 64), st.SystemID).Scan(&old, &name)
	if err == pgx.ErrNoRows {
		return nil // not ours, or being provisioned
	} else if err != nil {
		return err
	}
	if st.OK {
		if _, err := s.db.Exec(ctx, `insert into database_metrics values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) on conflict do nothing`,
			st.ID, st.TS, st.Connections, st.MaxConnections, st.TPS, st.QPS, st.CacheHit, st.Size, st.WaitingLocks, st.LagMs, st.ReplayRate); err != nil {
			return err
		}
	}
	if old != status && old != "unknown" {
		sev := map[string]string{"healthy": "info", "degraded": "warning", "down": "error"}[status]
		msg := fmt.Sprintf("Database %s is %s", name, status)
		if len(issues) > 0 {
			msg += ": " + strings.Join(issues, "; ")
		}
		s.event(ctx, ac.projectID, ac.serverID, sev, msg)
	}
	s.hub.publish(ac.projectID, "databases", st.ID)
	return nil
}
