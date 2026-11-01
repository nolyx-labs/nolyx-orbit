package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"orbit/internal/protocol"
)

var dbNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
var majorRe = regexp.MustCompile(`^(\d+)`)

func majorVersion(v string) string {
	if m := majorRe.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return "17"
}

const dbCols = `d.id, d.name, d.engine, d.host, d.port, d.username, d.dbname, d.sslmode, d.managed, d.version, d.role, d.status,
	d.system_id, d.error, d.stats, d.last_seen_at, d.created_at, d.primary_id, d.server_id, s.name as server_name, s.ip as server_ip,
	s.status as server_status, d.repl_password_encrypted is not null as replication_ready,
	(select row_to_json(m) from (select cpu, mem_used::float8 / nullif(mem_total,0) * 100 as mem, disk_used::float8 / nullif(disk_total,0) * 100 as disk, io_util
	   from server_metrics where server_id = s.id order by ts desc limit 1) m) as server_usage`

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select `+dbCols+` from databases d join servers s on s.id = d.server_id where d.project_id = $1 order by d.name`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getDatabase(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select `+dbCols+`,
		(select row_to_json(c) from (select interval_hours, retention, enabled, s3_encrypted is not null as s3, last_run_at from backup_configs where database_id = d.id) c) as backup_config
		from databases d join servers s on s.id = d.server_id where d.project_id = $1 and d.id::text = $2`, p.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) databaseMetrics(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	rng, step, agg, err := rangeAndAgg(r)
	if err != nil {
		return err
	}
	a := func(c string) string { return fmt.Sprintf(agg, c) }
	cols := []string{"connections", "tps", "qps", "cache_hit", "size", "waiting_locks", "lag_ms", "replay_rate"}
	sel, outCols := []string{}, []string{"coalesce(json_agg(extract(epoch from t)::bigint order by t), '[]') t"}
	for _, c := range cols {
		sel = append(sel, a(c)+" as "+c)
		outCols = append(outCols, fmt.Sprintf("coalesce(json_agg(round(%s::numeric, 2) order by t), '[]') %s", c, c))
	}
	where := "database_id in (select id from databases where project_id = $1)"
	args := []any{p.ID, step.String(), rng.String()}
	if id := r.URL.Query().Get("database"); id != "" {
		where = "database_id = (select id from databases where project_id = $1 and id::text = $4)"
		args = append(args, id)
	}
	// Across databases: connections are summed, lag takes the worst replica.
	out, err := s.one(r.Context(), fmt.Sprintf(`
		with b as (select date_bin($2::interval, ts, 'epoch') t, database_id, %s from database_metrics
		  where %s and ts > now() - $3::interval group by 1, 2),
		agg as (select t, sum(connections) connections, sum(tps) tps, sum(qps) qps, avg(cache_hit) cache_hit, sum(size) size,
		  sum(waiting_locks) waiting_locks, max(lag_ms) lag_ms, sum(replay_rate) replay_rate from b group by t)
		select %s from agg`, strings.Join(sel, ", "), where, strings.Join(outCols, ", ")), args...)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

type dbInput struct {
	Name     string `json:"name"`
	ServerID string `json:"server_id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
	// provisioning
	Version             string  `json:"version"`
	CPUs                float64 `json:"cpus"`
	MemoryMB            int     `json:"memory_mb"`
	ReplicationPassword string  `json:"replication_password"` // unmanaged primaries: password of an orbit_replicator role
}

func (in *dbInput) validate(connect bool) error {
	if !dbNameRe.MatchString(in.Name) {
		return errStatus(400, "name must be lowercase letters, digits and dashes (max 41)")
	}
	if in.Port < 1 || in.Port > 65535 {
		return errStatus(400, "invalid port")
	}
	if connect {
		if in.Host == "" || in.Username == "" || in.DBName == "" {
			return errStatus(400, "host, username and database are required")
		}
		switch in.SSLMode {
		case "":
			in.SSLMode = "prefer"
		case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		default:
			return errStatus(400, "invalid sslmode")
		}
	}
	if in.CPUs < 0 || in.CPUs > 256 || in.MemoryMB < 0 {
		return errStatus(400, "invalid resources")
	}
	return nil
}

// createDatabase connects an existing PostgreSQL for monitoring.
func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in dbInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.validate(true); err != nil {
		return err
	}
	if err := s.checkServer(r.Context(), p, in.ServerID); err != nil {
		return err
	}
	var replEnc []byte
	if in.ReplicationPassword != "" {
		replEnc = s.encrypt([]byte(in.ReplicationPassword))
	}
	out, err := s.one(r.Context(), `insert into databases (project_id, server_id, name, host, port, username, dbname, sslmode, password_encrypted, repl_password_encrypted)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) on conflict do nothing returning id`,
		p.ID, in.ServerID, in.Name, in.Host, in.Port, in.Username, in.DBName, in.SSLMode, s.encrypt([]byte(in.Password)), replEnc)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a database named %s already exists", in.Name)
	} else if err != nil {
		return err
	}
	s.auditProject(r, p, "database.connect", in.Name, map[string]any{"host": in.Host, "port": in.Port})
	s.pushConfig(r.Context(), in.ServerID)
	s.hub.publish(p.ID, "databases", "")
	writeJSON(w, 201, out)
	return nil
}

var pgVersions = map[string]bool{"15": true, "16": true, "17": true}

// provisionDatabase creates a managed PostgreSQL primary as a container on a server.
func (s *Server) provisionDatabase(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in dbInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.DBName == "" {
		in.DBName = "app"
	}
	if !pgVersions[in.Version] {
		return errStatus(400, "version must be 15, 16 or 17")
	}
	if err := in.validate(false); err != nil {
		return err
	}
	if err := s.checkServer(r.Context(), p, in.ServerID); err != nil {
		return err
	}
	if !s.hub.connected(in.ServerID) {
		return errOffline
	}
	pw, repl := randomSecret(), randomSecret()
	var dbID string
	err = s.db.QueryRow(r.Context(), `insert into databases (project_id, server_id, name, host, port, username, dbname, sslmode, password_encrypted,
		repl_password_encrypted, managed, version, status, role)
		values ($1,$2,$3,'127.0.0.1',$4,'postgres',$5,'disable',$6,$7,true,$8,'provisioning','primary') on conflict do nothing returning id::text`,
		p.ID, in.ServerID, in.Name, in.Port, in.DBName, s.encrypt([]byte(pw)), s.encrypt([]byte(repl)), in.Version).Scan(&dbID)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a database named %s already exists", in.Name)
	} else if err != nil {
		return err
	}
	s.auditProject(r, p, "database.provision", in.Name, map[string]any{"version": in.Version, "server_id": in.ServerID})
	opID, err := s.startOperation(r, p, "db.provision", in.Name, in.ServerID, dbID,
		[]string{"Pulling image", "Creating container", "Starting PostgreSQL", "Waiting for readiness"}, protocol.ActionDBProvision,
		protocol.PGArgs{Name: in.Name, Version: in.Version, Port: in.Port, Password: pw, ReplPassword: repl, DBName: in.DBName, CPUs: in.CPUs, MemoryMB: in.MemoryMB})
	if err != nil {
		s.db.Exec(context.WithoutCancel(r.Context()), `update databases set status = 'failed', error = $2 where id = $1`, dbID, err.Error())
		return err
	}
	writeJSON(w, 202, map[string]string{"id": dbID, "operation_id": opID})
	return nil
}

type dbRow struct {
	ID, Name, ServerID, Host, Username, DBName, Version, Role, Status, ServerIP string
	Port                                                                        int
	Managed                                                                     bool
	PrimaryID                                                                   *string
	Password, ReplPassword                                                      string
}

func (s *Server) loadDB(ctx context.Context, p project, id string) (dbRow, error) {
	var d dbRow
	var pw, repl []byte
	err := s.db.QueryRow(ctx, `select d.id::text, d.name, d.server_id::text, d.host, d.username, d.dbname, d.version, d.role, d.status, s.ip, d.port,
		d.managed, d.primary_id::text, d.password_encrypted, d.repl_password_encrypted
		from databases d join servers s on s.id = d.server_id where d.project_id = $1 and d.id::text = $2`, p.ID, id).
		Scan(&d.ID, &d.Name, &d.ServerID, &d.Host, &d.Username, &d.DBName, &d.Version, &d.Role, &d.Status, &d.ServerIP, &d.Port,
			&d.Managed, &d.PrimaryID, &pw, &repl)
	if err != nil {
		return d, err
	}
	b, err := s.decrypt(pw)
	if err != nil {
		return d, err
	}
	d.Password = string(b)
	if repl != nil {
		if b, err = s.decrypt(repl); err != nil {
			return d, err
		}
		d.ReplPassword = string(b)
	}
	return d, nil
}

// reachableHost is how another server reaches this database: loopback hosts are replaced by the server's IP.
func (d dbRow) reachableHost() string {
	if ip := net.ParseIP(d.Host); d.Host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return d.ServerIP
	}
	return d.Host
}

// confirmName enforces type-to-confirm for destructive database operations on the API too.
func confirmName(r *http.Request, name string) error {
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != name {
		return errStatus(400, "type the database name to confirm")
	}
	return nil
}

func (s *Server) deleteDatabase(w http.ResponseWriter, r *http.Request) error {
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
	if d.Managed {
		var replicas int
		s.db.QueryRow(r.Context(), `select count(*) from databases where primary_id::text = $1`, d.ID).Scan(&replicas)
		if replicas > 0 {
			return errStatus(409, "remove or promote this database's %d replica(s) first", replicas)
		}
		s.auditProject(r, p, "database.remove", d.Name, map[string]any{"managed": true})
		opID, err := s.startOperation(r, p, "db.remove", d.Name, d.ServerID, d.ID, []string{"Removing container"},
			protocol.ActionDBRemove, protocol.PGArgs{Name: d.Name})
		if err != nil {
			return err
		}
		writeJSON(w, 202, map[string]string{"operation_id": opID})
		return nil
	}
	if _, err := s.db.Exec(r.Context(), `delete from databases where id = $1`, d.ID); err != nil {
		return err
	}
	s.auditProject(r, p, "database.disconnect", d.Name, nil)
	s.pushConfig(r.Context(), d.ServerID)
	s.hub.publish(p.ID, "databases", "")
	w.WriteHeader(204)
	return nil
}
