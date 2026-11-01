// Managed PostgreSQL containers: provision, replicas, re-sync and removal.
package agent

import (
	"context"
	"fmt"
	"orbit/internal/protocol"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	pgNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	pgVersionRe = regexp.MustCompile(`^[0-9]{2}(\.[0-9]+)?$`)
	fileRe      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)
	safeRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`) // generated passwords; safe inside shell and SQL quoting
)

const pgData = "/var/lib/postgresql/data/pgdata"

func pgContainer(name string) string { return "orbit-pg-" + name }

func pgVolume(name string) string { return "orbit-pg-" + name + "-data" }

// pgInitScript runs once on a new managed primary: replication role, hba entry and pg_stat_statements.
const pgInitScript = `#!/bin/bash
set -e
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE orbit_replicator WITH REPLICATION LOGIN PASSWORD '${ORBIT_REPL_PASSWORD}';
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
SQL
echo "host replication orbit_replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"
`

// pgReplicaScript clones the primary on first start, then runs as a hot standby.
const pgReplicaScript = `set -e
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  mkdir -p "$PGDATA" && chown -R postgres:postgres "$(dirname "$PGDATA")" && chmod 700 "$PGDATA"
  gosu postgres pg_basebackup -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U orbit_replicator -D "$PGDATA" -R -X stream --checkpoint=fast -v
fi
exec gosu postgres postgres -c hot_standby=on -c shared_preload_libraries=pg_stat_statements -c wal_level=replica -c max_wal_senders=10 -c wal_keep_size=512MB
`

func resources(cpus float64, memMB int) map[string]any {
	hc := map[string]any{}
	if cpus > 0 {
		hc["NanoCpus"] = int64(cpus * 1e9)
	}
	if memMB > 0 {
		hc["Memory"] = int64(memMB) << 20
	}
	return hc
}

func (a *Agent) pgManage(ctx context.Context, action string, p protocol.PGArgs, progress func(protocol.Progress)) (any, error) {
	if !pgNameRe.MatchString(p.Name) {
		return nil, fmt.Errorf("invalid database name")
	}
	step := stepper(progress)
	name, vol := pgContainer(p.Name), pgVolume(p.Name)

	if action == protocol.ActionDBRemove || action == protocol.ActionDBResync {
		if err := step("Removing container", func() error {
			if err := a.docker.Action(ctx, protocol.ActionContainerRemove, name); err != nil && !strings.Contains(err.Error(), "404") {
				return err
			}
			return a.docker.RemoveVolume(ctx, vol)
		}); err != nil || action == protocol.ActionDBRemove {
			return nil, err
		}
	}

	if !pgVersionRe.MatchString(p.Version) || p.Port < 1 || p.Port > 65535 || !safeRe.MatchString(p.Password) || (p.ReplPassword != "" && !safeRe.MatchString(p.ReplPassword)) {
		return nil, fmt.Errorf("invalid provisioning arguments")
	}
	image := "postgres:" + p.Version
	replica := action != protocol.ActionDBProvision
	if a.docker.Exists(ctx, name) {
		return nil, fmt.Errorf("container %s already exists on this server", name)
	}
	if err := step("Pulling image", func() error { return a.docker.EnsureImage(ctx, image) }); err != nil {
		return nil, err
	}

	hc := resources(p.CPUs, p.MemoryMB)
	hc["Mounts"] = []map[string]any{{"Type": "volume", "Source": vol, "Target": "/var/lib/postgresql/data"}}
	hc["PortBindings"] = map[string][]map[string]string{"5432/tcp": {{"HostPort": strconv.Itoa(p.Port)}}}
	hc["RestartPolicy"] = map[string]string{"Name": "unless-stopped"}
	role := "primary"
	body := map[string]any{
		"Image":        image,
		"ExposedPorts": map[string]any{"5432/tcp": map[string]any{}},
		"HostConfig":   hc,
	}
	if replica {
		role = "replica"
		if p.PrimaryHost == "" || p.PrimaryPort == 0 || p.ReplPassword == "" {
			return nil, fmt.Errorf("replica requires primary host, port and replication password")
		}
		body["User"] = "root"
		body["Entrypoint"] = []string{"bash", "-c", pgReplicaScript}
		body["Env"] = []string{"PGDATA=" + pgData, "PGPASSWORD=" + p.ReplPassword,
			"PRIMARY_HOST=" + p.PrimaryHost, "PRIMARY_PORT=" + strconv.Itoa(p.PrimaryPort)}
	} else {
		db := p.DBName
		if db == "" {
			db = "app"
		}
		body["Env"] = []string{"PGDATA=" + pgData, "POSTGRES_PASSWORD=" + p.Password, "POSTGRES_DB=" + db, "ORBIT_REPL_PASSWORD=" + p.ReplPassword}
		body["Cmd"] = []string{"postgres", "-c", "wal_level=replica", "-c", "max_wal_senders=10", "-c", "hot_standby=on",
			"-c", "wal_keep_size=512MB", "-c", "shared_preload_libraries=pg_stat_statements"}
	}
	body["Labels"] = map[string]string{"orbit.managed": "true", "orbit.db": p.Name, "orbit.db.role": role}

	var id string
	if err := step("Creating container", func() (err error) {
		id, err = a.docker.CreateRaw(ctx, name, body)
		if err == nil && !replica {
			err = a.docker.Upload(ctx, id, "/docker-entrypoint-initdb.d", map[string]string{"orbit-init.sh": pgInitScript})
		}
		return err
	}); err != nil {
		return nil, err
	}
	startStep := "Starting PostgreSQL"
	if replica {
		startStep = "Cloning primary (pg_basebackup)"
	}
	if err := step(startStep, func() error { return a.docker.Action(ctx, protocol.ActionContainerStart, id) }); err != nil {
		return nil, err
	}
	err := step("Waiting for readiness", func() error {
		deadline := time.Now().Add(20 * time.Minute) // base backups of large databases take a while
		cfg := protocol.DBConfig{Host: "127.0.0.1", Port: p.Port, User: "postgres", Password: p.Password, DBName: "postgres", SSLMode: "disable"}
		for time.Now().Before(deadline) {
			time.Sleep(3 * time.Second)
			var insp struct{ State struct{ Running bool } }
			if err := a.docker.Inspect(ctx, id, &insp); err != nil {
				return err
			}
			if !insp.State.Running {
				out, _ := a.docker.rawLogs(ctx, id)
				return fmt.Errorf("container exited:\n%s", tail(out, 15))
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			c, err := pgx.Connect(cctx, connString(cfg))
			if err == nil {
				var recovery bool
				err = c.QueryRow(cctx, `select pg_is_in_recovery()`).Scan(&recovery)
				c.Close(cctx)
				cancel()
				if err == nil && recovery == replica {
					return nil
				}
				continue
			}
			cancel()
		}
		return fmt.Errorf("PostgreSQL was not ready in time")
	})
	if err != nil {
		a.docker.Action(context.WithoutCancel(ctx), protocol.ActionContainerRemove, id)
		a.docker.RemoveVolume(context.WithoutCancel(ctx), vol)
		return nil, err
	}
	return map[string]string{"container": name}, nil
}

// stepper wraps a step with running/done/failed progress frames.
func stepper(progress func(protocol.Progress)) func(string, func() error) error {
	return func(name string, fn func() error) error {
		progress(protocol.Progress{Step: name, Status: "running"})
		if err := fn(); err != nil {
			progress(protocol.Progress{Step: name, Status: "failed", Line: err.Error()})
			return err
		}
		progress(protocol.Progress{Step: name, Status: "done"})
		return nil
	}
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
