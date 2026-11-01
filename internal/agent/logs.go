package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"orbit/internal/protocol"
)

const (
	maxLogContainers = 60
	maxLinesPerPoll  = 500
	maxLineBytes     = 4096
)

// shipLogs polls running containers' logs every 3s with Docker's `since` cursor and ships new lines.
// Polling (rather than follow streams) keeps it stateless across reconnects: nothing is lost while
// disconnected as long as Docker still has the lines.
func (a *Agent) shipLogs(ctx context.Context) {
	since := map[string]string{}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		connected := a.conn != nil
		a.mu.Unlock()
		if !connected {
			continue
		}
		cs, err := a.docker.list(ctx, `{"status":["running"]}`)
		if err != nil {
			continue
		}
		next := map[string]string{}
		var lines []protocol.LogLine
		for i, c := range cs {
			if i >= maxLogContainers {
				break
			}
			name := ""
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}}
			if s, ok := since[c.ID]; ok {
				q.Set("since", s)
			} else {
				q.Set("tail", "50") // first sight: a little context, not the whole history
			}
			got, cursor, err := a.docker.logLines(ctx, c.ID, q)
			next[c.ID] = since[c.ID]
			if err != nil {
				continue
			}
			for _, l := range got {
				l.Container, l.App = name, c.Labels["orbit.app"]
				lines = append(lines, l)
			}
			if cursor != "" {
				next[c.ID] = cursor
			} else if next[c.ID] == "" {
				next[c.ID] = fmt.Sprintf("%d", time.Now().Unix())
			}
		}
		ok := true
		for len(lines) > 0 && ok {
			n := min(len(lines), 2000)
			ok = a.sendNow(ctx, protocol.TypeLogs, protocol.LogBatch{Lines: lines[:n]})
			lines = lines[n:]
		}
		if ok { // only advance cursors once the lines were delivered
			since = next
		}
	}
}

func (d *Docker) logLines(ctx context.Context, id string, q url.Values) ([]protocol.LogLine, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/v1.43/containers/"+url.PathEscape(id)+"/logs?"+q.Encode(), nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("logs: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", err
	}
	var out []protocol.LogLine
	cursor := ""
	for _, f := range frames(raw) {
		for _, line := range strings.Split(strings.TrimRight(string(f.data), "\n"), "\n") {
			tsStr, text, ok := strings.Cut(line, " ")
			ts, err := time.Parse(time.RFC3339Nano, tsStr)
			if !ok || err != nil {
				continue
			}
			if len(out) >= maxLinesPerPoll {
				return out, cursor, nil
			}
			if len(text) > maxLineBytes {
				text = text[:maxLineBytes] + "…"
			}
			stream := "stdout"
			if f.stream == 2 {
				stream = "stderr"
			}
			out = append(out, protocol.LogLine{ContainerID: id, TS: ts.UTC(), Stream: stream, Line: text})
			cursor = fmt.Sprintf("%d.%09d", ts.Unix(), ts.Nanosecond()+1)
		}
	}
	return out, cursor, nil
}

type frame struct {
	stream byte
	data   []byte
}

// frames splits Docker's multiplexed log stream; TTY containers have no headers.
func frames(b []byte) []frame {
	var out []frame
	for len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if 8+n > len(b) {
			n = len(b) - 8
		}
		// consecutive frames of the same stream can split a line; merge them
		if len(out) > 0 && out[len(out)-1].stream == b[0] && !bytes.HasSuffix(out[len(out)-1].data, []byte("\n")) {
			out[len(out)-1].data = append(out[len(out)-1].data, b[8:8+n]...)
		} else {
			out = append(out, frame{b[0], append([]byte(nil), b[8:8+n]...)})
		}
		b = b[8+n:]
	}
	if len(b) > 0 {
		out = append(out, frame{1, b})
	}
	return out
}
