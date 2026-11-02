// Server-wide backups: destinations, schedules, runs, retention and restores.
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"orbit/internal/backup"
	"orbit/internal/protocol"
)

const (
	keepBackups    = 7 // per service
	keepPreDeploys = 3 // per service, counted separately
)

// s3Providers maps each provider to its endpoint template (filled with the region). Empty: Amazon S3
// needs none, R2 and Other take the endpoint the user enters.
var s3Providers = map[string]string{
	"aws": "", "r2": "", "other": "",
	"b2":           "https://s3.%s.backblazeb2.com",
	"hetzner":      "https://%s.your-objectstorage.com",
	"digitalocean": "https://%s.digitaloceanspaces.com",
	"wasabi":       "https://s3.%s.wasabisys.com",
}

var (
	bucketRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	s3PrefixRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`)
	regionRe   = regexp.MustCompile(`^[a-z0-9-]{0,40}$`)
)

type s3Dest struct {
	Provider  string `json:"provider"`
	Region    string `json:"region"`
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// resolveS3 validates the form and works out the endpoint for the provider.
func resolveS3(in s3Dest) (protocol.S3Target, error) {
	t := protocol.S3Target{Bucket: in.Bucket, Prefix: strings.Trim(in.Prefix, "/"), Region: in.Region, AccessKey: in.AccessKey, SecretKey: in.SecretKey}
	tpl, ok := s3Providers[in.Provider]
	switch {
	case !ok:
		return t, errStatus(400, "unknown storage provider")
	case !bucketRe.MatchString(t.Bucket):
		return t, errStatus(400, "invalid bucket name")
	case !s3PrefixRe.MatchString(t.Prefix) || strings.Contains(t.Prefix, ".."):
		return t, errStatus(400, "the folder may only contain letters, digits, '.', '_', '-' and '/'")
	case !regionRe.MatchString(t.Region):
		return t, errStatus(400, "invalid region")
	}
	switch {
	case tpl != "":
		if t.Region == "" {
			return t, errStatus(400, "region is required")
		}
		t.Endpoint = fmt.Sprintf(tpl, t.Region)
	case in.Provider == "aws":
		if t.Region == "" {
			return t, errStatus(400, "region is required")
		}
	default:
		u, err := url.Parse(strings.TrimSuffix(in.Endpoint, "/"))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
			return t, errStatus(400, "endpoint must be a URL like https://<account>.r2.cloudflarestorage.com")
		}
		t.Endpoint = u.String()
		if t.Region == "" && in.Provider == "r2" {
			t.Region = "auto"
		}
	}
	return t, nil
}

func hostOf(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	return host
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// backupDest loads a server's destination and archive key. pgx.ErrNoRows: none configured.
func (s *Server) backupDest(ctx context.Context, serverID string) (protocol.BackupDestination, []byte, error) {
	var d protocol.BackupDestination
	var dest, addr, hostKey string
	var s3enc, keyEnc []byte
	var hasTarget bool
	err := s.db.QueryRow(ctx, `select destination, s3_encrypted, key_encrypted, target_addr, target_host_key, target_server_id is not null
		from server_backup_configs where server_id::text = $1`, serverID).Scan(&dest, &s3enc, &keyEnc, &addr, &hostKey, &hasTarget)
	if err != nil {
		return d, nil, err
	}
	key, err := s.decrypt(keyEnc)
	if err != nil {
		return d, nil, err
	}
	if dest == "s3" {
		b, err := s.decrypt(s3enc)
		if err != nil {
			return d, nil, err
		}
		d.S3 = &protocol.S3Target{}
		return d, key, json.Unmarshal(b, d.S3)
	}
	if !hasTarget {
		return d, nil, errStatus(400, "the target server was removed: choose a new backup destination")
	}
	d.Server = &protocol.SSHTarget{Addr: addr, HostKey: hostKey}
	return d, key, nil
}

// serverBackupArgs builds a run's arguments, including the server's managed PostgreSQL primaries
// (backed up with the existing pg_dump path rather than by copying a live data directory).
func (s *Server) serverBackupArgs(ctx context.Context, p project, serverID, kind string) (protocol.ServerBackupArgs, error) {
	args := protocol.ServerBackupArgs{Kind: kind, Folder: serverID}
	var err error
	if args.Dest, args.Key, err = s.backupDest(ctx, serverID); errors.Is(err, pgx.ErrNoRows) {
		return args, errStatus(400, "choose a backup destination for this server first")
	} else if err != nil {
		return args, err
	}
	rows, err := s.db.Query(ctx, `select id::text from databases where project_id = $1 and server_id::text = $2 and managed and role = 'primary'`, p.ID, serverID)
	if err != nil {
		return args, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return args, err
	}
	for _, id := range ids {
		d, err := s.loadDB(ctx, p, id)
		if err != nil {
			return args, err
		}
		args.Databases = append(args.Databases, protocol.ServiceDB{Name: d.Name, Version: majorVersion(d.Version),
			DB: protocol.DBConfig{Host: d.Host, Port: d.Port, User: d.Username, Password: d.Password, DBName: d.DBName}})
	}
	return args, nil
}

// runServerBackup starts a backup run of every service on the server and returns its operation id;
// r is nil when not triggered by a request. This is the "back up now" entry point for other callers
// (e.g. workflows): s.runServerBackup(ctx, nil, p, serverID, protocol.BackupManual).
func (s *Server) runServerBackup(ctx context.Context, r *http.Request, p project, serverID, kind string) (string, error) {
	if !s.hub.connected(serverID) {
		return "", errOffline
	}
	args, err := s.serverBackupArgs(ctx, p, serverID, kind)
	if err != nil {
		return "", err
	}
	var name string
	if err := s.db.QueryRow(ctx, `select name from servers where id::text = $1`, serverID).Scan(&name); err != nil {
		return "", err
	}
	id := newUUID()
	err = s.startOperationID(ctx, r, p, id, "server.backup", name, serverID, "", nil, protocol.ActionServerBackup, args)
	if isUniqueViolation(err) {
		return "", errStatus(409, "a backup of this server is already running")
	}
	return id, err
}

// preDeploySnapshot returns the snapshot a deploy takes before replacing the service's container
// (nil when the server has no backup destination). The agent reports it under its own operation.
func (s *Server) preDeploySnapshot(ctx context.Context, p project, serverID, app, userID string) (*protocol.ServerBackupArgs, error) {
	dest, key, err := s.backupDest(ctx, serverID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	args := &protocol.ServerBackupArgs{Kind: protocol.BackupPreDeploy, Dest: dest, Key: key, Folder: serverID, Only: "app/" + app, ReportID: newUUID()}
	// The ledger entry lets the agent's result for this id through; the signed deploy command authorizes it.
	if _, err := s.db.Exec(ctx, `insert into commands (id, server_id, action, args, requested_by) values ($1,$2,$3,$4,nullif($5,'')::uuid)`,
		args.ReportID, serverID, protocol.ActionServerBackup, mustJSON(map[string]string{"pre_deploy": app}), userID); err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `insert into operations (id, project_id, server_id, kind, target, triggered_by) values ($1,$2,$3,'server.snapshot',$4,nullif($5,'')::uuid)`,
		args.ReportID, p.ID, serverID, app, userID)
	return args, err
}

// finishServerBackup records a run's archives, settles the run's status and applies retention.
// It returns the final status and error ("" status: an empty pre-deploy snapshot, nothing to record).
// finishOperation then writes the "<kind> of <target> succeeded|failed" event: that is the
// backup succeeded/failed point; the recovery event is written here.
func (s *Server) finishServerBackup(ctx context.Context, ac *agentConn, res protocol.Result, kind, status string) (string, string) {
	var out protocol.ServerBackupResult
	json.Unmarshal(res.Output, &out)
	errMsg := res.Error
	if errMsg == "" {
		errMsg = out.Error
	}
	failed, firstErr := 0, ""
	for _, b := range out.Backups {
		st := "succeeded"
		if b.Error != "" {
			st = "failed"
			if failed++; firstErr == "" {
				firstErr = b.Service + ": " + b.Error
			}
		}
		if _, err := s.db.Exec(ctx, `insert into server_backups (server_id, operation_id, service, kind, status, file, location, size, duration_ms, error, created_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now() - make_interval(secs => $9::float8 / 1000))`,
			ac.serverID, res.ID, trunc(b.Service, 100), b.Kind, st, trunc(b.File, 255), trunc(b.Location, 500), b.Size, b.Millis, trunc(b.Error, 2000)); err != nil {
			s.log.Error("record server backup", "err", err)
		}
	}
	if errMsg == "" && failed > 0 {
		errMsg = fmt.Sprintf("%d of %d services failed. %s", failed, len(out.Backups), firstErr)
	}
	if kind == "server.snapshot" && len(out.Backups) == 0 && errMsg == "" {
		s.db.Exec(ctx, `delete from operations where id::text = $1`, res.ID) // the service has no volumes
		return "", ""
	}
	if errMsg != "" && status == "succeeded" {
		status = "failed"
		s.db.Exec(ctx, `update operations set status = 'failed', error = $2 where id::text = $1`, res.ID, errMsg)
	}
	if kind == "server.backup" && status == "succeeded" {
		var prev string
		s.db.QueryRow(ctx, `select status from operations where server_id::text = $1 and kind = 'server.backup' and id::text <> $2 and status <> 'running'
			order by created_at desc limit 1`, ac.serverID, res.ID).Scan(&prev)
		if prev == "failed" {
			var name string
			s.db.QueryRow(ctx, `select name from servers where id::text = $1`, ac.serverID).Scan(&name)
			s.event(ctx, ac.projectID, ac.serverID, "info", fmt.Sprintf("Backups of %s are working again", name))
		}
	}
	s.pruneServerBackups(ctx, ac.serverID)
	s.hub.publish(ac.projectID, "server-backups", ac.serverID)
	return status, errMsg
}

type archiveRow struct{ ID, Service, Kind, File string }

// expiredBackups picks what falls out of retention from a server's succeeded archives (newest first):
// each service keeps its last 7 backups, with pre-deploy snapshots counted separately (last 3).
func expiredBackups(rows []archiveRow) []archiveRow {
	seen := map[[2]string]int{}
	var out []archiveRow
	for _, r := range rows {
		group, keep := "", keepBackups
		if r.Kind == protocol.BackupPreDeploy {
			group, keep = r.Kind, keepPreDeploys
		}
		k := [2]string{r.Service, group}
		if seen[k]++; seen[k] > keep {
			out = append(out, r)
		}
	}
	return out
}

// pruneServerBackups drops expired archives from history and asks the agent to delete the files.
func (s *Server) pruneServerBackups(ctx context.Context, serverID string) {
	s.db.Exec(ctx, `delete from server_backups where server_id::text = $1 and status = 'failed' and created_at < now() - interval '30 days'`, serverID)
	rows, err := s.db.Query(ctx, `select id::text, service, kind, file from server_backups where server_id::text = $1 and status = 'succeeded'
		order by created_at desc`, serverID)
	if err != nil {
		return
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[archiveRow])
	expired := expiredBackups(all)
	if err != nil || len(expired) == 0 {
		return
	}
	ids, files := make([]string, len(expired)), make([]string, len(expired))
	for i, r := range expired {
		ids[i], files[i] = r.ID, r.File
	}
	dest, _, err := s.backupDest(ctx, serverID)
	if err != nil {
		return
	}
	if _, err := s.db.Exec(ctx, `delete from server_backups where id::text = any($1)`, ids); err != nil {
		return
	}
	// shortcut: a failed delete leaves the files behind; retry from history if storage cost matters.
	if _, err := s.sendCommand(context.WithoutCancel(ctx), newUUID(), serverID, "", protocol.ActionServerBackupPrune,
		protocol.BackupPruneArgs{Dest: dest, Folder: serverID, Files: files}, false); err != nil {
		s.log.Warn("prune server backups", "server", serverID, "err", err)
	}
}

type dueBackup struct{ ServerID, ProjectID, OrgID string }

func (s *Server) dueServerBackups(ctx context.Context) ([]dueBackup, error) {
	rows, err := s.db.Query(ctx, `select c.server_id::text, s.project_id::text, p.org_id::text from server_backup_configs c
		join servers s on s.id = c.server_id join projects p on p.id = s.project_id
		where c.schedule <> 'manual' and s.status = 'online'
		  and (c.last_run_at is null or c.last_run_at < now() - case c.schedule when 'daily' then interval '1 day' else interval '7 days' end)
		  and not exists (select 1 from operations o where o.server_id = c.server_id and o.kind = 'server.backup' and o.status = 'running')`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[dueBackup])
}

// scheduleServerBackups starts due daily and weekly runs. Called by the worker.
func (s *Server) scheduleServerBackups(ctx context.Context) {
	due, err := s.dueServerBackups(ctx)
	if err != nil {
		s.log.Error("schedule server backups", "err", err)
		return
	}
	for _, d := range due {
		if !s.hub.connected(d.ServerID) {
			continue
		}
		s.db.Exec(ctx, `update server_backup_configs set last_run_at = now() where server_id::text = $1`, d.ServerID)
		if _, err := s.runServerBackup(ctx, nil, project{ID: d.ProjectID, OrgID: d.OrgID}, d.ServerID, protocol.BackupScheduled); err != nil {
			var name string
			s.db.QueryRow(ctx, `select name from servers where id::text = $1`, d.ServerID).Scan(&name)
			s.event(ctx, d.ProjectID, d.ServerID, "error", fmt.Sprintf("Scheduled backup of %s could not start: %v", name, err))
		}
	}
}

func (s *Server) getServerBackups(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select
		(select row_to_json(c) from (select c.schedule, c.destination, c.s3_provider, c.target_server_id, t.name as target_name, c.target_addr, c.last_run_at, c.updated_at
		   from server_backup_configs c left join servers t on t.id = c.target_server_id where c.server_id = s.id) c) as config,
		(select coalesce(json_agg(b), '[]') from (select id, service, kind, status, file, location, size, duration_ms, error, created_at, operation_id
		   from server_backups where server_id = s.id order by created_at desc limit 200) b) as backups,
		(select coalesce(json_agg(o), '[]') from (select id, kind, target, status, error, created_at, finished_at
		   from operations where server_id = s.id and kind like 'server.%' order by created_at desc limit 20) o) as runs
		from servers s where s.project_id = $1 and s.id::text = $2`, p.ID, r.PathValue("sid"))
	if err != nil {
		return err
	}
	// The S3 settings (minus the keys) live in the encrypted blob, so they're added here for the form.
	var res map[string]any
	json.Unmarshal(out, &res)
	if cfg, _ := res["config"].(map[string]any); cfg != nil && cfg["destination"] == "s3" {
		if d, _, err := s.backupDest(r.Context(), r.PathValue("sid")); err == nil && d.S3 != nil {
			cfg["s3"] = map[string]string{"bucket": d.S3.Bucket, "prefix": d.S3.Prefix, "region": d.S3.Region, "endpoint": d.S3.Endpoint}
		}
	}
	writeJSON(w, 200, res)
	return nil
}

// listServerBackupOverview: every server's destination, schedule and last run, plus recent runs.
func (s *Server) listServerBackupOverview(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	servers, err := s.list(r.Context(), `select s.id, s.name, s.status as server_status, c.schedule, c.destination, c.s3_provider, t.name as target_name,
		lb.created_at as last_backup_at, lr.status as last_run_status, lr.error as last_run_error, lr.created_at as last_run_at
		from servers s left join server_backup_configs c on c.server_id = s.id left join servers t on t.id = c.target_server_id
		left join lateral (select created_at from server_backups where server_id = s.id and status = 'succeeded' order by created_at desc limit 1) lb on true
		left join lateral (select status, error, created_at from operations where server_id = s.id and kind = 'server.backup'
		  order by created_at desc limit 1) lr on true
		where s.project_id = $1 order by s.name`, p.ID)
	if err != nil {
		return err
	}
	runs, err := s.list(r.Context(), `select o.id, o.kind, o.target, o.status, o.error, o.created_at, o.finished_at, o.server_id, s.name as server_name,
		extract(epoch from coalesce(o.finished_at, now()) - o.created_at)::int as duration,
		(select count(*) from server_backups b where b.operation_id = o.id) as archives,
		(select coalesce(sum(size), 0) from server_backups b where b.operation_id = o.id) as size
		from operations o join servers s on s.id = o.server_id
		where o.project_id = $1 and o.kind like 'server.%' order by o.created_at desc limit 50`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]json.RawMessage{"servers": servers, "runs": runs})
	return nil
}

func (s *Server) putServerBackupConfig(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	ctx, sid := r.Context(), r.PathValue("sid")
	var name string
	if err := s.db.QueryRow(ctx, `select name from servers where project_id = $1 and id::text = $2`, p.ID, sid).Scan(&name); err != nil {
		return err
	}
	var in struct {
		Schedule       string  `json:"schedule"`
		Destination    string  `json:"destination"`
		S3             *s3Dest `json:"s3"`
		TargetServerID string  `json:"target_server_id"`
		TargetAddr     string  `json:"target_addr"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Schedule != "daily" && in.Schedule != "weekly" && in.Schedule != "manual" {
		return errStatus(400, "schedule must be daily, weekly or manual")
	}
	var oldS3 []byte
	var oldTarget *string
	s.db.QueryRow(ctx, `select s3_encrypted, target_server_id::text from server_backup_configs where server_id::text = $1`, sid).Scan(&oldS3, &oldTarget)

	var s3enc []byte
	var provider, addr, hostKey string
	var target *string
	free := int64(-1)
	switch in.Destination {
	case "s3":
		if in.S3 == nil {
			return errStatus(400, "S3 settings are required")
		}
		t, err := resolveS3(*in.S3)
		if err != nil {
			return err
		}
		if t.AccessKey == "" && t.SecretKey == "" && oldS3 != nil { // keys are write-only: empty keeps the stored ones
			var old protocol.S3Target
			if b, err := s.decrypt(oldS3); err == nil && json.Unmarshal(b, &old) == nil {
				t.AccessKey, t.SecretKey = old.AccessKey, old.SecretKey
			}
		}
		if t.AccessKey == "" || t.SecretKey == "" {
			return errStatus(400, "access key and secret key are required")
		}
		s.auditProject(r, p, "server.backup_config", name, map[string]any{"schedule": in.Schedule, "destination": "s3", "provider": in.S3.Provider, "bucket": t.Bucket})
		// A small test object proves the keys can write to the bucket before anything relies on them.
		c := backup.S3{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket, AccessKey: t.AccessKey, SecretKey: t.SecretKey,
			Client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: s.safeDialer().DialContext}}}
		testKey := backup.ObjectKey(t.Prefix, sid, ".orbit-write-test")
		if err := c.Upload(ctx, testKey, strings.NewReader("orbit"), 5); err != nil {
			return errStatus(400, "could not write a test file to the bucket: %v", err)
		}
		c.Delete(ctx, testKey) // keys may be allowed to write but not delete; that's fine
		s3enc, provider = s.encrypt(mustJSON(t)), in.S3.Provider
	case "server":
		if in.TargetServerID == sid {
			return errStatus(400, "a server can't back up to itself")
		}
		var ip string
		if err := s.db.QueryRow(ctx, `select ip from servers where project_id = $1 and id::text = $2`, p.ID, in.TargetServerID).Scan(&ip); err != nil {
			return errStatus(400, "choose a target server in this project")
		}
		addr = in.TargetAddr
		if addr == "" {
			addr = net.JoinHostPort(ip, "22")
		}
		if host, port, err := net.SplitHostPort(addr); err != nil || host == "" || strings.ContainsAny(host, " /") {
			return errStatus(400, "SSH address must be host:port")
		} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return errStatus(400, "SSH address must be host:port")
		}
		if !s.hub.connected(sid) || !s.hub.connected(in.TargetServerID) {
			return errStatus(409, "both servers must be online to set up server-to-server backups")
		}
		s.auditProject(r, p, "server.backup_config", name, map[string]any{"schedule": in.Schedule, "destination": "server", "target": in.TargetServerID, "addr": addr})
		uid := currentUser(r).ID
		res, err := s.sendCommand(ctx, newUUID(), sid, uid, protocol.ActionServerBackupKey, struct{}{}, true)
		if err == nil && !res.OK {
			err = errStatus(400, "this server could not create its backup key: %s", res.Error)
		}
		if err != nil {
			return err
		}
		var key struct {
			PublicKey string `json:"public_key"`
		}
		json.Unmarshal(res.Output, &key)
		res, err = s.sendCommand(ctx, newUUID(), in.TargetServerID, uid, protocol.ActionServerBackupAuthorize,
			protocol.BackupAuthorizeArgs{SourceID: sid, PublicKey: key.PublicKey}, true)
		if err == nil && !res.OK {
			err = errStatus(400, "the target server could not be set up: %s", res.Error)
		}
		if err != nil {
			return err
		}
		var auth protocol.BackupAuthorizeResult
		json.Unmarshal(res.Output, &auth)
		if auth.HostKey == "" {
			return errStatus(502, "the target server did not report its SSH host key")
		}
		hostKey, free, target = auth.HostKey, auth.FreeBytes, &in.TargetServerID
	default:
		return errStatus(400, "choose S3-compatible storage or another server")
	}
	if oldTarget != nil && (target == nil || *oldTarget != *target) && s.hub.connected(*oldTarget) {
		s.sendCommand(context.WithoutCancel(ctx), newUUID(), *oldTarget, currentUser(r).ID, protocol.ActionServerBackupAuthorize,
			protocol.BackupAuthorizeArgs{SourceID: sid}, false) // best effort: revoke the old target
	}
	archiveKey := make([]byte, 32)
	rand.Read(archiveKey)
	if _, err := s.db.Exec(ctx, `insert into server_backup_configs
		(server_id, schedule, destination, s3_provider, s3_encrypted, target_server_id, target_addr, target_host_key, key_encrypted)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (server_id) do update set schedule = $2, destination = $3, s3_provider = $4, s3_encrypted = $5,
		  target_server_id = $6, target_addr = $7, target_host_key = $8, updated_at = now()`,
		sid, in.Schedule, in.Destination, provider, s3enc, target, addr, hostKey, s.encrypt(archiveKey)); err != nil {
		return err
	}
	s.hub.publish(p.ID, "server-backups", sid)
	writeJSON(w, 200, map[string]any{"free_bytes": free})
	return nil
}

func (s *Server) createServerBackup(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from servers where project_id = $1 and id::text = $2`, p.ID, r.PathValue("sid")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "server.backup", name, nil)
	id, err := s.runServerBackup(r.Context(), r, p, r.PathValue("sid"), protocol.BackupManual)
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": id})
	return nil
}

func (s *Server) restoreServerBackup(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	ctx, sid := r.Context(), r.PathValue("sid")
	var service, file, location string
	if err := s.db.QueryRow(ctx, `select b.service, b.file, b.location from server_backups b join servers s on s.id = b.server_id
		where s.project_id = $1 and s.id::text = $2 and b.id::text = $3 and b.status = 'succeeded'`, p.ID, sid, r.PathValue("bid")).
		Scan(&service, &file, &location); err != nil {
		return err
	}
	typ, name, _ := strings.Cut(service, "/")
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != name {
		return errStatus(400, "type the service name to confirm")
	}
	if !s.hub.connected(sid) {
		return errOffline
	}
	args, err := s.serverBackupArgs(ctx, p, sid, protocol.BackupPreRestore)
	if err != nil {
		return err
	}
	if d := args.Dest; (d.S3 != nil && !strings.HasPrefix(location, "s3://"+d.S3.Bucket+"/")) ||
		(d.Server != nil && !strings.HasPrefix(location, hostOf(d.Server.Addr)+":")) {
		return errStatus(400, "this backup is stored in a destination that is no longer set up for this server")
	}
	args.Only = service
	plan := []string{"Snapshot of current data", "Downloading and decrypting backup", "Stopping service", "Replacing data", "Starting service"}
	if typ == "db" && slices.ContainsFunc(args.Databases, func(d protocol.ServiceDB) bool { return d.Name == name }) {
		plan = []string{plan[0], plan[1], "Restoring (pg_restore --clean)"}
	}
	s.auditProject(r, p, "server.backup_restore", service, map[string]any{"server_id": sid, "file": file})
	id := newUUID()
	err = s.startOperationID(ctx, r, p, id, "server.restore", service, sid, "", plan, protocol.ActionServerRestore,
		protocol.ServerRestoreArgs{ServerBackupArgs: args, File: file})
	if isUniqueViolation(err) {
		return errStatus(409, "a restore of %s is already running", name)
	}
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": id})
	return nil
}
