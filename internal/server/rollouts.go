// OS update rollouts: install updates across servers, all at once or in batches.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type rolloutInput struct {
	Scope         string                   `json:"scope"` // all | security | packages
	Packages      []protocol.OSPackageSpec `json:"packages"`
	ServerIDs     []string                 `json:"server_ids"` // empty: every server with the update waiting
	BatchSize     int                      `json:"batch_size"` // 0: all at once
	StopOnFailure bool                     `json:"stop_on_failure"`
	Reboot        string                   `json:"reboot"` // never | if_required
	StartsAt      *time.Time               `json:"starts_at"`
	DryRun        bool                     `json:"dry_run"` // only return the servers it would update
}

func (in *rolloutInput) validate() error {
	if !oneOf(in.Scope, []string{"all", "security", "packages"}) {
		return errStatus(400, "choose all updates, security updates or specific packages")
	}
	if in.Scope != "packages" {
		in.Packages = nil
	} else if len(in.Packages) == 0 || len(in.Packages) > 500 {
		return errStatus(400, "choose at least one package")
	}
	for _, p := range in.Packages {
		if !protocol.ValidPackage(p.Name) {
			return errStatus(400, "invalid package name %q", p.Name)
		}
		if p.Version != "" && !protocol.ValidVersion(p.Version) {
			return errStatus(400, "invalid version %q for %s", p.Version, p.Name)
		}
	}
	if !oneOf(in.Reboot, []string{"never", "if_required"}) {
		return errStatus(400, "restart must be never or if_required")
	}
	if in.BatchSize < 0 || in.BatchSize > 1000 || len(in.ServerIDs) > 1000 {
		return errStatus(400, "batch size must be 0–1000")
	}
	if in.StartsAt != nil && (in.StartsAt.Before(time.Now().Add(-time.Minute)) || in.StartsAt.After(time.Now().AddDate(0, 0, 60))) {
		return errStatus(400, "start time must be between now and 60 days from now")
	}
	return nil
}

// rolloutTargetsSQL: picked servers, or every server whose last check has a matching update.
// $1 project, $2 picked server ids, $3 scope, $4 package names.
const rolloutTargetsSQL = `select sv.id, sv.name, sv.status from servers sv left join os_update_checks c on c.server_id = sv.id
where sv.project_id = $1 and case when cardinality($2::text[]) > 0 then sv.id::text = any($2::text[])
  else exists (select 1 from jsonb_array_elements(c.packages) e
    where $3 = 'all' or ($3 = 'security' and (e->>'security')::bool) or ($3 = 'packages' and e->>'name' = any($4::text[]))) end`

func (in rolloutInput) targetArgs(projectID string) []any {
	names := []string{}
	for _, p := range in.Packages {
		names = append(names, p.Name)
	}
	ids := in.ServerIDs
	if ids == nil {
		ids = []string{}
	}
	return []any{projectID, ids, in.Scope, names}
}

// startRollout records a rollout and its servers in order. It returns pgx.ErrNoRows when no server matches.
func (s *Server) startRollout(ctx context.Context, projectID, userID string, in rolloutInput) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `insert into os_rollouts (project_id, scope, packages, batch_size, stop_on_failure, reboot, starts_at, created_by)
			values ($1,$2,$3,$4,$5,$6,coalesce($7, now()),nullif($8,'')::uuid) returning id::text`,
			projectID, in.Scope, mustJSON(in.Packages), in.BatchSize, in.StopOnFailure, in.Reboot, in.StartsAt, userID).Scan(&id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `insert into os_rollout_servers (rollout_id, server_id, position)
			select $5, id, row_number() over (order by name) from (`+rolloutTargetsSQL+`) t`, append(in.targetArgs(projectID), id)...)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		return err
	})
	return id, err
}

func (s *Server) createRollout(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	var in rolloutInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	if len(in.ServerIDs) > 0 {
		var n int
		s.db.QueryRow(r.Context(), `select count(*) from servers where project_id = $1 and id::text = any($2)`, p.ID, in.ServerIDs).Scan(&n)
		if n != len(in.ServerIDs) {
			return errStatus(400, "server not found")
		}
	}
	if in.DryRun {
		out, err := s.list(r.Context(), rolloutTargetsSQL+` order by sv.name`, in.targetArgs(p.ID)...)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]json.RawMessage{"servers": out})
		return nil
	}
	if roleRank[p.Role] < roleRank["member"] {
		return errStatus(403, "requires member role")
	}
	id, err := s.startRollout(r.Context(), p.ID, currentUser(r).ID, in)
	if errors.Is(err, pgx.ErrNoRows) {
		return errStatus(400, "no servers have these updates waiting")
	} else if err != nil {
		return err
	}
	s.auditProject(r, p, "os_rollout.create", in.Scope, map[string]any{"id": id, "packages": in.Packages, "servers": in.ServerIDs,
		"batch_size": in.BatchSize, "stop_on_failure": in.StopOnFailure, "reboot": in.Reboot, "starts_at": in.StartsAt})
	if in.StartsAt == nil || !in.StartsAt.After(time.Now()) {
		s.claimRollouts(r.Context())
	}
	s.hub.publish(p.ID, "updates", id)
	writeJSON(w, 201, map[string]string{"id": id})
	return nil
}

func (s *Server) listRollouts(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select o.id, o.scope, o.packages, o.batch_size, o.stop_on_failure, o.reboot, o.starts_at, o.status,
		o.created_at, o.finished_at, u.email created_by,
		(select coalesce(json_agg(x order by x.position), '[]') from (select rs.server_id, sv.name, rs.position, rs.status, rs.result,
		   rs.operation_id, rs.started_at, rs.finished_at from os_rollout_servers rs join servers sv on sv.id = rs.server_id
		   where rs.rollout_id = o.id) x) servers
		from os_rollouts o left join users u on u.id = o.created_by
		where o.project_id = $1 and ($2 = '' or exists (select 1 from os_rollout_servers rs where rs.rollout_id = o.id and rs.server_id::text = $2))
		order by o.created_at desc limit 50`, p.ID, r.URL.Query().Get("server"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// cancelRollout stops servers that haven't started; ones already installing finish.
func (s *Server) cancelRollout(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var id, was string
	err = s.db.QueryRow(r.Context(), `update os_rollouts o set status = 'cancelled',
		finished_at = case when o.status = 'scheduled' then now() end
		from (select id, status from os_rollouts where project_id = $1 and id::text = $2) old
		where o.id = old.id and o.status in ('scheduled','running') returning o.id::text, old.status`, p.ID, r.PathValue("id")).Scan(&id, &was)
	if err == pgx.ErrNoRows {
		return errStatus(409, "this rollout has already finished")
	} else if err != nil {
		return err
	}
	if was == "scheduled" {
		s.db.Exec(r.Context(), `update os_rollout_servers set status = 'skipped', result = 'Rollout cancelled' where rollout_id = $1 and status = 'waiting'`, id)
	}
	s.auditProject(r, p, "os_rollout.cancel", id, nil)
	s.hub.publish(p.ID, "updates", id)
	w.WriteHeader(204)
	return nil
}

// claimRollouts starts due rollouts, and picks up running ones whose runner stopped (Orbit restarted).
func (s *Server) claimRollouts(ctx context.Context) {
	rows, err := s.db.Query(ctx, `update os_rollouts set status = 'running', heartbeat_at = now()
		where (status = 'scheduled' and starts_at <= now()) or (status = 'running' and heartbeat_at < now() - interval '2 minutes')
		returning id::text`)
	if err != nil {
		s.log.Error("claim rollouts", "err", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		go s.runRollout(context.WithoutCancel(ctx), id, s.installOnServer)
	}
}

// installFunc installs updates on one server and blocks until it finishes. Tests swap it for a fake.
type installFunc func(ctx context.Context, projectID, serverID, opID string, args protocol.OSInstallArgs) (protocol.OSInstallResult, error)

// runRollout works through a rollout's servers batch by batch.
func (s *Server) runRollout(ctx context.Context, id string, install installFunc) {
	var projectID, scope, reboot string
	var specs []protocol.OSPackageSpec
	var batch int
	var stop bool
	if err := s.db.QueryRow(ctx, `select project_id::text, scope, packages, batch_size, stop_on_failure, reboot from os_rollouts where id = $1`, id).
		Scan(&projectID, &scope, &specs, &batch, &stop, &reboot); err != nil {
		s.log.Error("load rollout", "id", id, "err", err)
		return
	}
	hb, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hb.Done():
				return
			case <-t.C:
				s.db.Exec(hb, `update os_rollouts set heartbeat_at = now() where id = $1`, id)
			}
		}
	}()
	skipWaiting := func(why string) {
		s.db.Exec(ctx, `update os_rollout_servers set status = 'skipped', result = $2, finished_at = now() where rollout_id = $1 and status = 'waiting'`, id, why)
	}
	for {
		var status string
		s.db.QueryRow(ctx, `select status from os_rollouts where id = $1`, id).Scan(&status)
		if status == "cancelled" {
			skipWaiting("Rollout cancelled")
			break
		}
		// Servers still running come first: they were started before a restart of Orbit.
		rows, err := s.db.Query(ctx, `select rs.server_id::text, sv.name, rs.status, sv.status, coalesce(rs.operation_id::text, ''),
			coalesce(c.packages, '[]') from os_rollout_servers rs join servers sv on sv.id = rs.server_id left join os_update_checks c on c.server_id = rs.server_id
			where rs.rollout_id = $1 and rs.status in ('waiting','running') order by rs.status = 'running' desc, rs.position limit nullif($2, 0)`, id, batch)
		if err != nil {
			s.log.Error("rollout batch", "id", id, "err", err)
			return // picked up again once the heartbeat goes stale
		}
		type target struct {
			id, name, status, online, opID string
			pending                        []protocol.OSPackage
		}
		var targets []target
		for rows.Next() {
			var t target
			rows.Scan(&t.id, &t.name, &t.status, &t.online, &t.opID, &t.pending)
			targets = append(targets, t)
		}
		rows.Close()
		if len(targets) == 0 {
			break
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		failed := false
		finish := func(t target, st, result string, rebooting bool) {
			s.db.Exec(ctx, `update os_rollout_servers set status = $3, result = $4, rebooting = $5, finished_at = now() where rollout_id = $1 and server_id::text = $2`,
				id, t.id, st, trunc(result, 1000), rebooting)
			s.hub.publish(projectID, "updates", id)
		}
		for _, t := range targets {
			args := protocol.OSInstallArgs{Scope: scope, Reboot: reboot, Packages: serverPackages(specs, t.pending)}
			if t.status == "waiting" {
				if t.online != "online" {
					finish(t, "skipped", "Server was offline", false)
					continue
				}
				if scope == "packages" && len(args.Packages) == 0 {
					finish(t, "skipped", "None of these updates are waiting on this server", false)
					continue
				}
				t.opID = newUUID()
				s.db.Exec(ctx, `update os_rollout_servers set status = 'running', operation_id = $3, started_at = now() where rollout_id = $1 and server_id::text = $2`, id, t.id, t.opID)
				s.hub.publish(projectID, "updates", id)
			}
			wg.Go(func() {
				res, err := install(ctx, projectID, t.id, t.opID, args)
				if err != nil {
					mu.Lock()
					failed = true
					mu.Unlock()
					finish(t, "failed", err.Error(), false)
					return
				}
				s.storeCheck(ctx, t.id, res.Check)
				msg := "Updates installed"
				switch {
				case res.Rebooting:
					s.db.Exec(ctx, `update servers set alerts_held_until = now() + interval '10 minutes' where id::text = $1`, t.id)
					msg += ", restarting the server"
				case res.Check.RebootRequired:
					msg += "; a restart is needed to finish"
				}
				finish(t, "done", msg, res.Rebooting)
			})
		}
		wg.Wait()
		if failed && stop {
			skipWaiting("Not started because an earlier server failed")
			break
		}
	}
	var final string
	var done, bad, skipped int
	s.db.QueryRow(ctx, `update os_rollouts set status = case when status = 'cancelled' then status
		  when exists (select 1 from os_rollout_servers where rollout_id = $1 and status = 'failed') then 'failed' else 'done' end,
		finished_at = now() where id = $1 returning status,
		(select count(*) filter (where status = 'done') from os_rollout_servers where rollout_id = $1),
		(select count(*) filter (where status = 'failed') from os_rollout_servers where rollout_id = $1),
		(select count(*) filter (where status = 'skipped') from os_rollout_servers where rollout_id = $1)`, id).Scan(&final, &done, &bad, &skipped)
	sev := map[string]string{"done": "info", "failed": "error", "cancelled": "warning"}[final]
	s.event(ctx, projectID, "", sev, fmt.Sprintf("OS update rollout %s: %d updated, %d failed, %d skipped", map[string]string{
		"done": "finished", "failed": "failed", "cancelled": "cancelled"}[final], done, bad, skipped))
	s.hub.publish(projectID, "updates", id)
}

// serverPackages narrows the chosen packages to one server: latest-version updates only where they're
// waiting there; exact versions always, since they may be downgrades.
func serverPackages(specs []protocol.OSPackageSpec, pending []protocol.OSPackage) []protocol.OSPackageSpec {
	waiting := map[string]bool{}
	for _, p := range pending {
		waiting[p.Name] = true
	}
	var out []protocol.OSPackageSpec
	for _, p := range specs {
		if p.Version != "" || waiting[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// installOnServer runs the install as an operation (so its log streams to the operation page) and
// waits for it. When resumed after a restart it only waits for the operation already started.
func (s *Server) installOnServer(ctx context.Context, projectID, serverID, opID string, args protocol.OSInstallArgs) (protocol.OSInstallResult, error) {
	var res protocol.OSInstallResult
	var name string
	var started bool
	s.db.QueryRow(ctx, `select name, exists(select 1 from operations where id::text = $2) from servers where id::text = $1`, serverID, opID).Scan(&name, &started)
	if !started {
		if err := s.startOperationID(ctx, nil, project{ID: projectID}, opID, "os.update", name, serverID, "", protocol.OSInstallPlan, protocol.ActionOSInstall, args); err != nil {
			return res, err
		}
	}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		var status string
		var errText *string
		var output []byte
		if err := s.db.QueryRow(ctx, `select status, error, output from operations where id::text = $1`, opID).Scan(&status, &errText, &output); err != nil {
			return res, err
		}
		if status != "running" {
			json.Unmarshal(output, &res)
			if status == "failed" {
				if errText == nil {
					return res, errors.New("the update failed")
				}
				return res, errors.New(*errText)
			}
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-t.C:
		}
	}
}
