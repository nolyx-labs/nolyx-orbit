package server

import (
	"context"
	"fmt"
	"time"
)

// RunWorker runs periodic jobs until ctx ends. Single instance; see docs/ARCHITECTURE.md §7.
func (s *Server) RunWorker(ctx context.Context) {
	fast, slow := time.NewTicker(30*time.Second), time.NewTicker(time.Hour)
	defer fast.Stop()
	defer slow.Stop()
	s.retention(ctx)
	go s.runMonitors(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			s.markStale(ctx)
			s.evaluateAlerts(ctx)
			s.updateStatusPages(ctx)
			s.scheduleBackups(ctx)
			s.expireProvisions(ctx)
		case <-slow.C:
			s.retention(ctx)
		}
	}
}

// markStale catches agents whose connection died without a close frame.
func (s *Server) markStale(ctx context.Context) {
	rows, err := s.db.Query(ctx, `update servers set status = 'offline'
		where status = 'online' and last_seen_at < now() - interval '45 seconds' returning id::text, project_id::text, name`)
	if err != nil {
		s.log.Error("mark stale", "err", err)
		return
	}
	type srv struct{ id, project, name string }
	var stale []srv
	for rows.Next() {
		var x srv
		rows.Scan(&x.id, &x.project, &x.name)
		stale = append(stale, x)
	}
	rows.Close()
	for _, x := range stale {
		s.hub.mu.Lock()
		if ac := s.hub.agents[x.id]; ac != nil {
			ac.ws.CloseNow()
		}
		s.hub.mu.Unlock()
		s.event(ctx, x.project, x.id, "warning", fmt.Sprintf("Server %s stopped sending heartbeats", x.name))
		s.hub.publish(x.project, "servers", x.id)
	}
}

func (s *Server) retention(ctx context.Context) {
	for _, q := range []string{
		`delete from server_metrics where ts < now() - interval '30 days'`,
		`delete from container_metrics where ts < now() - interval '30 days'`,
		`delete from database_metrics where ts < now() - interval '30 days'`,
		`delete from app_metrics where ts < now() - interval '30 days'`,
		`delete from log_lines where ts < now() - interval '7 days'`,
		`delete from monitor_checks where ts < now() - interval '30 days'`,
		`delete from status_subscribers where confirmed_at is null and created_at < now() - interval '7 days'`,
		`delete from sessions where expires_at < now()`,
		`delete from password_resets where expires_at < now()`,
		`delete from events where created_at < now() - interval '90 days'`,
		`delete from commands where created_at < now() - interval '90 days'`,
		`delete from invites where expires_at < now() - interval '7 days'`,
		`update commands set status = 'failed', error = 'no result from agent', completed_at = now()
		   where status = 'pending' and created_at < now() - interval '1 hour'`,
		`update deployments set status = 'failed', error = 'agent stopped reporting', finished_at = now()
		   where status = 'running' and created_at < now() - interval '1 hour'`,
		`update operations set status = 'failed', error = 'agent stopped reporting', finished_at = now()
		   where status = 'running' and created_at < now() - interval '1 hour'`,
	} {
		if _, err := s.db.Exec(ctx, q); err != nil {
			s.log.Error("retention", "err", err)
		}
	}
}
