// Copying a project's logs into a PostgreSQL database the customer owns, for longer retention or their own analysis.
// The worker copies new lines every 30 seconds into an orbit_logs table it creates there.
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	forwardBatch   = 5000
	forwardBatches = 20 // per project per tick, so a long backlog catches up without holding up the worker
)

const orbitLogsDDL = `create table if not exists orbit_logs (
	id bigint primary key, ts timestamptz not null, project text not null, server text not null, container text not null,
	unit text not null, app text not null, stream text not null, level text not null, line text not null);
	create index if not exists orbit_logs_ts on orbit_logs (ts)`

// destConn connects through the same dialer as webhooks, so a destination can't reach private addresses.
func (s *Server) destConn(ctx context.Context, url string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil || !(strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")) {
		return nil, errStatus(400, "enter a PostgreSQL URL like postgres://user:password@db.example.com:5432/logs")
	}
	cfg.DialFunc = s.safeDialer().DialContext
	cfg.ConnectTimeout = 10 * time.Second
	return pgx.ConnectConfig(ctx, cfg)
}

func (s *Server) getLogDestination(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select true as configured, d.enabled, d.last_at, d.last_error, d.created_at,
		(select count(*) from log_lines l where l.project_id = d.project_id and l.id > d.forwarded_id) behind
		from log_destinations d where d.project_id = $1`, p.ID)
	if err == pgx.ErrNoRows {
		writeJSON(w, 200, map[string]bool{"configured": false})
		return nil
	} else if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// putLogDestination saves the destination after connecting and creating the table, so mistakes show up right away.
// A blank URL keeps the saved one (it holds a password, so it's never shown again).
func (s *Server) putLogDestination(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		URL     string `json:"url"`
		Enabled bool   `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.URL = strings.TrimSpace(in.URL); in.URL != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		conn, err := s.destConn(ctx, in.URL)
		if err != nil {
			if _, ok := err.(apiError); ok {
				return err
			}
			return errStatus(400, "couldn't connect: %v", err)
		}
		_, err = conn.Exec(ctx, orbitLogsDDL)
		conn.Close(context.WithoutCancel(ctx))
		if err != nil {
			return errStatus(400, "connected, but couldn't create the orbit_logs table: %v", err)
		}
		_, err = s.db.Exec(r.Context(), `insert into log_destinations (project_id, url_encrypted, enabled) values ($1, $2, $3)
			on conflict (project_id) do update set url_encrypted = $2, enabled = $3, last_error = ''`, p.ID, s.encrypt([]byte(in.URL)), in.Enabled)
		if err != nil {
			return err
		}
	} else if tag, err := s.db.Exec(r.Context(), `update log_destinations set enabled = $2 where project_id = $1`, p.ID, in.Enabled); err != nil {
		return err
	} else if tag.RowsAffected() == 0 {
		return errStatus(400, "enter the database URL")
	}
	s.auditProject(r, p, "logs.destination.save", "", map[string]any{"enabled": in.Enabled, "url_changed": in.URL != ""})
	return s.getLogDestination(w, r)
}

func (s *Server) deleteLogDestination(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(r.Context(), `delete from log_destinations where project_id = $1`, p.ID); err != nil {
		return err
	}
	s.auditProject(r, p, "logs.destination.delete", "", nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) runLogForwarding(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.forwardLogs(ctx)
		}
	}
}

func (s *Server) forwardLogs(ctx context.Context) {
	// shortcut: ids are handed out before their batch commits, so only ids seen a tick ago (30s) are copied; a
	// batch taking longer than that to commit would be skipped. Track commit order (e.g. pg_snapshot) if that shows up.
	var high int64
	if err := s.db.QueryRow(ctx, `select coalesce(max(id), 0) from log_lines`).Scan(&high); err != nil {
		s.log.Error("forward logs", "err", err)
		return
	}
	rows, err := s.db.Query(ctx, `select d.project_id::text, p.name, d.url_encrypted, d.forwarded_id, d.safe_id
		from log_destinations d join projects p on p.id = d.project_id where d.enabled`)
	if err != nil {
		s.log.Error("forward logs", "err", err)
		return
	}
	type dest struct {
		project, name   string
		url             []byte
		forwarded, safe int64
	}
	var ds []dest
	for rows.Next() {
		var d dest
		rows.Scan(&d.project, &d.name, &d.url, &d.forwarded, &d.safe)
		ds = append(ds, d)
	}
	rows.Close()
	for _, d := range ds {
		fctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		n, err := s.forwardProject(fctx, d.project, d.name, d.url, d.forwarded, d.safe)
		cancel()
		if err != nil {
			s.db.Exec(ctx, `update log_destinations set safe_id = $2, last_error = $3 where project_id::text = $1`, d.project, high, trunc(err.Error(), 500))
		} else {
			s.db.Exec(ctx, `update log_destinations set safe_id = $2, last_error = '', last_at = case when $3 then now() else last_at end
				where project_id::text = $1`, d.project, high, n > 0)
		}
	}
}

// forwardProject copies lines with forwarded < id <= safe and returns how many it copied. Inserts skip ids already
// there, so a retry after a half-finished tick doesn't fail.
func (s *Server) forwardProject(ctx context.Context, project, projectName string, enc []byte, forwarded, safe int64) (int, error) {
	if forwarded >= safe {
		return 0, nil
	}
	url, err := s.decrypt(enc)
	if err != nil {
		return 0, err
	}
	conn, err := s.destConn(ctx, string(url))
	if err != nil {
		return 0, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, orbitLogsDDL); err != nil {
		return 0, err
	}
	copied := 0
	for range forwardBatches {
		rows, err := s.db.Query(ctx, `select l.id, l.ts, sv.name, l.container, l.unit, l.app, l.stream, l.level, l.line
			from log_lines l join servers sv on sv.id = l.server_id
			where l.project_id::text = $1 and l.id > $2 and l.id <= $3 order by l.id limit $4`, project, forwarded, safe, forwardBatch)
		if err != nil {
			return copied, err
		}
		var ids []int64
		var ts []time.Time
		var srv, ctr, unit, app, stream, level, line []string
		for rows.Next() {
			var i int64
			var t time.Time
			var a, b, c, d, e, f, g string
			if err := rows.Scan(&i, &t, &a, &b, &c, &d, &e, &f, &g); err != nil {
				rows.Close()
				return copied, err
			}
			ids, ts = append(ids, i), append(ts, t)
			srv, ctr, unit, app, stream, level, line = append(srv, a), append(ctr, b), append(unit, c), append(app, d), append(stream, e), append(level, f), append(line, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return copied, err
		}
		next := safe // nothing left in range: skip past other projects' ids
		if len(ids) > 0 {
			if _, err := conn.Exec(ctx, `insert into orbit_logs (id, ts, project, server, container, unit, app, stream, level, line)
				select i, t, $3, s, c, u, a, st, lv, ln from unnest($1::bigint[], $2::timestamptz[], $4::text[], $5::text[], $6::text[],
				  $7::text[], $8::text[], $9::text[], $10::text[]) as x(i, t, s, c, u, a, st, lv, ln)
				on conflict (id) do nothing`, ids, ts, projectName, srv, ctr, unit, app, stream, level, line); err != nil {
				return copied, err
			}
			copied += len(ids)
			if len(ids) == forwardBatch {
				next = ids[len(ids)-1]
			}
		}
		if _, err := s.db.Exec(ctx, `update log_destinations set forwarded_id = $2 where project_id::text = $1`, project, next); err != nil {
			return copied, err
		}
		if forwarded = next; forwarded >= safe {
			break
		}
	}
	return copied, nil
}
