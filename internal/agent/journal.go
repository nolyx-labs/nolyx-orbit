// systemd journal logs of the services chosen in Orbit, shipped with the container logs.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"orbit/internal/protocol"
)

const maxJournalLines = 1000

func (a *Agent) setLogUnits(units []string) {
	var ok []string
	for _, u := range units {
		if protocol.ValidUnit(u) {
			ok = append(ok, u)
		}
	}
	a.mu.Lock()
	a.logUnits = ok
	a.mu.Unlock()
}

// shipJournal polls the journal every 3s from the last delivered cursor. Like shipLogs, polling keeps it stateless
// across reconnects: nothing is lost while disconnected as long as the journal still has the entries.
func (a *Agent) shipJournal(ctx context.Context) {
	cursor, lastErr := "", ""
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		units, connected := a.logUnits, a.conn != nil
		a.mu.Unlock()
		if !connected || len(units) == 0 || a.cfg.HostRoot != "" { // in a container the host's journal isn't reachable
			continue
		}
		lines, next, err := readJournal(ctx, units, cursor)
		if err != nil {
			if err.Error() != lastErr {
				a.log.Warn("journal", "err", err)
				lastErr = err.Error()
			}
			continue
		}
		if len(lines) == 0 || a.sendNow(ctx, protocol.TypeLogs, protocol.LogBatch{Lines: lines}) {
			cursor = next
		}
	}
}

func readJournal(ctx context.Context, units []string, cursor string) ([]protocol.LogLine, string, error) {
	args := []string{"--no-pager", "-o", "json", "--output-fields=MESSAGE,PRIORITY,_SYSTEMD_UNIT,UNIT"}
	if cursor == "" {
		args = append(args, "-n", "50") // first sight: a little context, not the whole history
	} else {
		args = append(args, "--after-cursor", cursor)
	}
	for _, u := range units {
		args = append(args, "-u", u)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	lines, next := parseJournal(out, maxJournalLines)
	cmd.Process.Kill() // the rest is read on the next poll, from the cursor
	cmd.Wait()
	if next == "" {
		next = cursor
	}
	return lines, next, nil
}

// parseJournal reads up to max entries of `journalctl -o json` and returns them with the last entry's cursor.
func parseJournal(r io.Reader, max int) ([]protocol.LogLine, string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var out []protocol.LogLine
	var red redactor
	cursor := ""
	for len(out) < max && sc.Scan() {
		var e struct {
			Cursor   string          `json:"__CURSOR"`
			TS       string          `json:"__REALTIME_TIMESTAMP"`
			Unit     string          `json:"_SYSTEMD_UNIT"`
			About    string          `json:"UNIT"` // systemd's own messages about a unit ("Started nginx.service")
			Priority string          `json:"PRIORITY"`
			Message  json.RawMessage `json:"MESSAGE"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Cursor == "" {
			continue
		}
		cursor = e.Cursor
		usec, err := strconv.ParseInt(e.TS, 10, 64)
		if err != nil {
			continue
		}
		var text string
		if json.Unmarshal(e.Message, &text) != nil {
			var raw []byte // non-UTF-8 messages come as an array of bytes
			var ints []int
			if json.Unmarshal(e.Message, &ints) != nil {
				continue
			}
			for _, b := range ints {
				raw = append(raw, byte(b))
			}
			text = string(raw)
		}
		text = red.line(text)
		if len(text) > maxLineBytes {
			text = text[:maxLineBytes] + "…"
		}
		unit := e.Unit
		if unit == "" || unit == "init.scope" {
			unit = e.About
		}
		out = append(out, protocol.LogLine{Unit: unit, TS: time.UnixMicro(usec).UTC(), Stream: "journal", Level: journalLevel(e.Priority), Line: text})
	}
	return out, cursor
}

func journalLevel(priority string) string {
	switch priority {
	case "0", "1", "2", "3":
		return "error"
	case "4":
		return "warn"
	case "7":
		return "debug"
	}
	return "info"
}
