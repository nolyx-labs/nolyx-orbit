// Central container logs: ingest, level detection and search.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"orbit/internal/protocol"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	errorRe = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|exception|traceback|critical)\b`)
	warnRe  = regexp.MustCompile(`(?i)\b(warn|warning)\b`)
	debugRe = regexp.MustCompile(`(?i)\b(debug|trace)\b`)
)

// logLevel guesses a level from the line text; structured JSON logs carrying "level" win.
func logLevel(line string) string {
	if strings.HasPrefix(line, "{") {
		var m struct {
			Level    string `json:"level"`
			Severity string `json:"severity"`
		}
		if json.Unmarshal([]byte(line), &m) == nil {
			switch l := strings.ToLower(m.Level + m.Severity); {
			case strings.HasPrefix(l, "err"), strings.HasPrefix(l, "fatal"), strings.HasPrefix(l, "crit"), strings.HasPrefix(l, "panic"):
				return "error"
			case strings.HasPrefix(l, "warn"):
				return "warn"
			case strings.HasPrefix(l, "debug"), strings.HasPrefix(l, "trace"):
				return "debug"
			case l != "":
				return "info"
			}
		}
	}
	switch {
	case errorRe.MatchString(line):
		return "error"
	case warnRe.MatchString(line):
		return "warn"
	case debugRe.MatchString(line):
		return "debug"
	}
	return "info"
}

func (s *Server) storeLogs(ctx context.Context, ac *agentConn, b protocol.LogBatch) error {
	if len(b.Lines) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(b.Lines))
	for _, l := range b.Lines {
		rows = append(rows, []any{ac.projectID, ac.serverID, trunc(l.ContainerID, 64), trunc(l.Container, 255), trunc(l.App, 64), l.TS,
			l.Stream, logLevel(l.Line), strings.ToValidUTF8(strings.ReplaceAll(l.Line, "\x00", ""), "�")})
	}
	_, err := s.db.CopyFrom(ctx, pgx.Identifier{"log_lines"},
		[]string{"project_id", "server_id", "container_id", "container", "app", "ts", "stream", "level", "line"}, pgx.CopyFromRows(rows))
	if err == nil {
		s.hub.publish(ac.projectID, "logs", "")
	}
	return err
}

func (s *Server) searchLogs(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	rng, ok := ranges[q.Get("range")]
	if !ok {
		rng = time.Hour
	}
	var levels []string
	if l := q.Get("level"); l != "" {
		levels = strings.Split(l, ",")
	}
	out, err := s.list(r.Context(), `
		select l.id, l.ts, l.level, l.stream, l.line, l.container, l.container_id, l.app, l.server_id, s.name as server_name
		from log_lines l join servers s on s.id = l.server_id
		where l.project_id = $1 and l.ts > now() - $2::interval
		  and ($3 = '' or l.line ilike '%' || $3 || '%')
		  and (coalesce(cardinality($4::text[]), 0) = 0 or l.level = any($4))
		  and ($5 = '' or l.server_id::text = $5) and ($6 = '' or l.app = $6) and ($7 = '' or l.container = $7)
		  and ($8 = 0 or l.id < $8)
		order by l.ts desc, l.id desc limit 300`,
		p.ID, rng.String(), escapeLike(q.Get("q")), levels, q.Get("server"), q.Get("app"), q.Get("container"), atoi(q.Get("before")))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func atoi(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
