package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"orbit/internal/protocol"
)

// dbMonitor keeps one connection per configured database and samples it every 15s.
type dbMonitor struct {
	mu   sync.Mutex
	log  *slog.Logger
	mons map[string]*dbMon
}

type dbMon struct {
	cfg        protocol.DBConfig
	conn       *pgx.Conn
	prevAt     time.Time
	prevXact   float64
	prevCalls  float64
	prevReplay float64
}

func newDBMonitor(log *slog.Logger) *dbMonitor {
	return &dbMonitor{log: log, mons: map[string]*dbMon{}}
}

func (m *dbMonitor) configure(cfgs []protocol.DBConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]protocol.DBConfig{}
	for _, c := range cfgs {
		want[c.ID] = c
	}
	for id, mon := range m.mons {
		if c, ok := want[id]; !ok || c != mon.cfg {
			if mon.conn != nil {
				mon.conn.Close(context.Background())
			}
			delete(m.mons, id)
		}
	}
	for id, c := range want {
		if m.mons[id] == nil {
			m.mons[id] = &dbMon{cfg: c}
		}
	}
}

func connString(c protocol.DBConfig) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c.User, c.Password),
		Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Path: "/" + c.DBName}
	q := url.Values{"sslmode": {c.SSLMode}, "application_name": {"orbit-agent"}, "connect_timeout": {"5"}}
	if c.SSLMode == "" {
		q.Set("sslmode", "prefer")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (m *dbMonitor) get(id string) *dbMon {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mons[id]
}

func (d *dbMon) connect(ctx context.Context) (*pgx.Conn, error) {
	if d.conn != nil && !d.conn.IsClosed() {
		return d.conn, nil
	}
	c, err := pgx.Connect(ctx, connString(d.cfg))
	if err != nil {
		return nil, err
	}
	d.conn = c
	return c, nil
}

func (a *Agent) reportDatabases(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.dbs.mu.Lock()
		mons := make([]*dbMon, 0, len(a.dbs.mons))
		for _, m := range a.dbs.mons {
			mons = append(mons, m)
		}
		a.dbs.mu.Unlock()
		for _, m := range mons {
			qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			st := m.sample(qctx)
			cancel()
			a.sendNow(ctx, protocol.TypeDBStats, st)
		}
	}
}

// sample collects one snapshot. Only the connection is required; every other query is best effort
// because monitoring users often lack privileges for some views.
func (d *dbMon) sample(ctx context.Context) protocol.DBStats {
	now := time.Now().UTC()
	st := protocol.DBStats{ID: d.cfg.ID, TS: now, SlowQueries: []protocol.SlowQuery{}, Tables: []protocol.TableSize{}, Replicas: []protocol.ReplicaInfo{}}
	c, err := d.connect(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	var recovery bool
	if err := c.QueryRow(ctx, `select current_setting('server_version'), current_setting('max_connections')::int, pg_is_in_recovery(),
		(select count(*) from pg_stat_activity)::int, pg_database_size(current_database())`).
		Scan(&st.Version, &st.MaxConnections, &recovery, &st.Connections, &st.Size); err != nil {
		c.Close(ctx)
		d.conn = nil
		st.Error = err.Error()
		return st
	}
	st.OK = true
	st.Role = "primary"
	if recovery {
		st.Role = "replica"
	}
	c.QueryRow(ctx, `select system_identifier::text from pg_control_system()`).Scan(&st.SystemID)
	c.QueryRow(ctx, `select count(*)::int from pg_locks where not granted`).Scan(&st.WaitingLocks)

	elapsed := now.Sub(d.prevAt).Seconds()
	rate := func(cur float64, prev *float64) *float64 {
		var out *float64
		if !d.prevAt.IsZero() && elapsed > 0 && cur >= *prev {
			v := (cur - *prev) / elapsed
			out = &v
		}
		*prev = cur
		return out
	}
	var xact, hit, read float64
	if c.QueryRow(ctx, `select coalesce(sum(xact_commit + xact_rollback),0)::float8, coalesce(sum(blks_hit),0)::float8, coalesce(sum(blks_read),0)::float8 from pg_stat_database`).
		Scan(&xact, &hit, &read) == nil {
		st.TPS = rate(xact, &d.prevXact)
		if hit+read > 0 {
			v := hit / (hit + read) * 100
			st.CacheHit = &v
		}
	}
	var calls float64
	if c.QueryRow(ctx, `select coalesce(sum(calls),0)::float8 from pg_stat_statements`).Scan(&calls) == nil {
		st.QPS = rate(calls, &d.prevCalls)
	}

	if rows, err := c.Query(ctx, `select pid, extract(epoch from now() - query_start)::float8, coalesce(state,''), left(query, 400), coalesce(wait_event_type,'')
		from pg_stat_activity where state = 'active' and pid <> pg_backend_pid() and query_start < now() - interval '1 second'
		order by query_start limit 10`); err == nil {
		for rows.Next() {
			var q protocol.SlowQuery
			if rows.Scan(&q.PID, &q.Seconds, &q.State, &q.Query, &q.WaitType) == nil {
				st.SlowQueries = append(st.SlowQueries, q)
			}
		}
		rows.Close()
	}
	if rows, err := c.Query(ctx, `select schemaname || '.' || relname, pg_total_relation_size(relid), n_live_tup
		from pg_stat_user_tables order by 2 desc limit 15`); err == nil {
		for rows.Next() {
			var t protocol.TableSize
			if rows.Scan(&t.Name, &t.Bytes, &t.Rows) == nil {
				st.Tables = append(st.Tables, t)
			}
		}
		rows.Close()
	}

	if !recovery {
		c.QueryRow(ctx, `select pg_current_wal_lsn()::text`).Scan(&st.WALPosition)
		if rows, err := c.Query(ctx, `select coalesce(application_name,''), coalesce(client_addr::text,''), coalesce(state,''), coalesce(sync_state,''),
			coalesce(replay_lsn::text,''), coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn),0)::bigint,
			extract(epoch from replay_lag)::float8 * 1000 from pg_stat_replication`); err == nil {
			for rows.Next() {
				var r protocol.ReplicaInfo
				if rows.Scan(&r.Name, &r.ClientAddr, &r.State, &r.SyncState, &r.ReplayLSN, &r.LagBytes, &r.ReplayLagMs) == nil {
					st.Replicas = append(st.Replicas, r)
				}
			}
			rows.Close()
		}
	} else {
		var lag *float64
		var replayed float64
		c.QueryRow(ctx, `select coalesce(pg_last_wal_replay_lsn()::text,''),
			(case when pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() then 0
			     else extract(epoch from now() - pg_last_xact_replay_timestamp()) * 1000 end)::float8,
			coalesce(pg_wal_lsn_diff(pg_last_wal_replay_lsn(), '0/0'),0)::float8`).Scan(&st.WALPosition, &lag, &replayed)
		st.LagMs = lag
		st.ReplayRate = rate(replayed, &d.prevReplay)
		var w protocol.WalReceiver
		if c.QueryRow(ctx, `select coalesce(status,''), coalesce(sender_host,''), coalesce(sender_port,0) from pg_stat_wal_receiver`).
			Scan(&w.Status, &w.SenderHost, &w.SenderPort) == nil {
			st.Receiver = &w
		}
	}
	d.prevAt = now
	return st
}

func (m *dbMonitor) promote(ctx context.Context, id string) error {
	d := m.get(id)
	if d == nil {
		return errors.New("database is not monitored by this agent")
	}
	c, err := d.connect(ctx)
	if err != nil {
		return err
	}
	var recovery bool
	if err := c.QueryRow(ctx, `select pg_is_in_recovery()`).Scan(&recovery); err != nil {
		return err
	}
	if !recovery {
		return errors.New("database is already a primary")
	}
	var ok bool
	if err := c.QueryRow(ctx, `select pg_promote(true, 60)`).Scan(&ok); err != nil {
		return fmt.Errorf("pg_promote: %w", err)
	}
	if !ok {
		return errors.New("promotion did not complete within 60s")
	}
	return nil
}
