// Database backups, restores and schedules.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
	"strings"
	"time"
)

type s3Input struct {
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	Region    string `json:"region"`
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func (s *Server) putBackupConfig(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	d, err := s.loadDB(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	var in struct {
		IntervalHours int      `json:"interval_hours"`
		Retention     int      `json:"retention"`
		Enabled       bool     `json:"enabled"`
		S3            *s3Input `json:"s3"`
		KeepS3        bool     `json:"keep_s3"` // leave stored S3 credentials untouched
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.IntervalHours < 1 || in.IntervalHours > 720 || in.Retention < 1 || in.Retention > 365 {
		return errStatus(400, "interval must be 1–720 hours and retention 1–365 backups")
	}
	var s3 []byte
	if in.S3 != nil {
		if in.S3.Bucket == "" || in.S3.AccessKey == "" || in.S3.SecretKey == "" {
			return errStatus(400, "S3 bucket, access key and secret key are required")
		}
		if in.S3.Region == "" {
			in.S3.Region = "us-east-1"
		}
		b, _ := json.Marshal(protocol.S3Target(*in.S3))
		s3 = s.encrypt(b)
	}
	_, err = s.db.Exec(r.Context(), `insert into backup_configs (database_id, interval_hours, retention, enabled, s3_encrypted) values ($1,$2,$3,$4,$5)
		on conflict (database_id) do update set interval_hours = $2, retention = $3, enabled = $4,
		  s3_encrypted = case when $6 then backup_configs.s3_encrypted else $5 end`,
		d.ID, in.IntervalHours, in.Retention, in.Enabled, s3, in.KeepS3)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "database.backup_config", d.Name, map[string]any{"interval_hours": in.IntervalHours, "retention": in.Retention, "s3": in.S3 != nil || in.KeepS3})
	w.WriteHeader(204)
	return nil
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select b.id, b.file, b.size, b.location, b.status, b.scheduled, b.created_at, b.finished_at, d.name as database, d.id as database_id
		from backups b join databases d on d.id = b.database_id
		where d.project_id = $1 and ($2 = '' or d.id::text = $2) order by b.created_at desc limit 200`, p.ID, r.URL.Query().Get("database"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) backupArgs(ctx context.Context, d dbRow) (protocol.BackupArgs, error) {
	args := protocol.BackupArgs{
		DB:      protocol.DBConfig{Host: d.Host, Port: d.Port, User: d.Username, Password: d.Password, DBName: d.DBName},
		Version: majorVersion(d.Version),
	}
	var enc []byte
	s.db.QueryRow(ctx, `select s3_encrypted from backup_configs where database_id::text = $1`, d.ID).Scan(&enc)
	if enc != nil {
		b, err := s.decrypt(enc)
		if err != nil {
			return args, err
		}
		args.S3 = &protocol.S3Target{}
		json.Unmarshal(b, args.S3)
	}
	return args, nil
}

// runBackup starts a backup operation; r is nil for scheduled runs.
func (s *Server) runBackup(ctx context.Context, r *http.Request, p project, d dbRow, scheduled bool) (string, error) {
	if !s.hub.connected(d.ServerID) {
		return "", errOffline
	}
	args, err := s.backupArgs(ctx, d)
	if err != nil {
		return "", err
	}
	args.File = fmt.Sprintf("%s-%s.dump", d.Name, time.Now().UTC().Format("20060102-150405"))
	var retention int
	if s.db.QueryRow(ctx, `select retention from backup_configs where database_id::text = $1`, d.ID).Scan(&retention) == nil {
		rows, _ := s.db.Query(ctx, `select file from backups where database_id::text = $1 and status = 'succeeded' order by created_at desc offset $2`, d.ID, max(retention-1, 0))
		for rows != nil && rows.Next() {
			var f string
			rows.Scan(&f)
			args.Prune = append(args.Prune, f)
		}
		if rows != nil {
			rows.Close()
		}
	}
	plan := []string{"Dumping database"}
	if args.S3 != nil {
		plan = append(plan, "Uploading to S3")
	}
	if len(args.Prune) > 0 {
		plan = append(plan, "Applying retention")
	}
	id := newUUID()
	if _, err := s.db.Exec(ctx, `insert into backups (id, database_id, file, scheduled) values ($1,$2,$3,$4)`, id, d.ID, args.File, scheduled); err != nil {
		return "", err
	}
	if len(args.Prune) > 0 {
		s.db.Exec(ctx, `delete from backups where database_id::text = $1 and file = any($2)`, d.ID, args.Prune)
	}
	return id, s.startOperationID(ctx, r, p, id, "db.backup", d.Name, d.ServerID, d.ID, plan, protocol.ActionDBBackup, args)
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	d, err := s.loadDB(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	s.auditProject(r, p, "database.backup", d.Name, nil)
	id, err := s.runBackup(r.Context(), r, p, d, false)
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": id})
	return nil
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	d, err := s.loadDB(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := confirmName(r, d.Name); err != nil {
		return err
	}
	if d.Role == "replica" {
		return errStatus(400, "restore into the primary; replicas follow it")
	}
	var file, location string
	if err := s.db.QueryRow(r.Context(), `select file, location from backups where id::text = $1 and database_id::text = $2 and status = 'succeeded'`,
		r.PathValue("bid"), d.ID).Scan(&file, &location); err != nil {
		return err
	}
	args, err := s.backupArgs(r.Context(), d)
	if err != nil {
		return err
	}
	args.File = file
	if !strings.HasPrefix(location, "s3://") {
		args.S3 = nil
	}
	plan := []string{"Restoring (pg_restore --clean)"}
	if args.S3 != nil {
		plan = append([]string{"Downloading from S3"}, plan...)
	}
	s.auditProject(r, p, "database.restore", d.Name, map[string]any{"file": file})
	opID, err := s.startOperation(r, p, "db.restore", d.Name, d.ServerID, d.ID, plan, protocol.ActionDBRestore, args)
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": opID})
	return nil
}

// scheduleBackups starts due scheduled backups. Called by the worker.
func (s *Server) scheduleBackups(ctx context.Context) {
	rows, err := s.db.Query(ctx, `select c.database_id::text, d.project_id::text, p.org_id::text from backup_configs c
		join databases d on d.id = c.database_id join projects p on p.id = d.project_id
		where c.enabled and d.status in ('healthy','degraded') and d.role = 'primary'
		  and (c.last_run_at is null or c.last_run_at < now() - make_interval(hours => c.interval_hours))`)
	if err != nil {
		s.log.Error("schedule backups", "err", err)
		return
	}
	type due struct{ db, project, org string }
	var list []due
	for rows.Next() {
		var d due
		rows.Scan(&d.db, &d.project, &d.org)
		list = append(list, d)
	}
	rows.Close()
	for _, x := range list {
		p := project{ID: x.project, OrgID: x.org}
		d, err := s.loadDB(ctx, p, x.db)
		if err != nil || !s.hub.connected(d.ServerID) {
			continue
		}
		s.db.Exec(ctx, `update backup_configs set last_run_at = now() where database_id::text = $1`, x.db)
		if _, err := s.runBackup(ctx, nil, p, d, true); err != nil {
			s.event(ctx, x.project, d.ServerID, "error", fmt.Sprintf("Scheduled backup of %s could not start: %v", d.Name, err))
		}
	}
}
