// The local action log: one JSON line per command, kept on the server so its owner can audit what the
// control plane asked for without trusting the control plane's own records.
package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"orbit/internal/protocol"
)

const actionLogMax = 10 << 20 // rotated to actions.log.1 past this size

type actionLog struct {
	mu   sync.Mutex
	path string
}

type actionEntry struct {
	Time       time.Time `json:"time"`
	ID         string    `json:"id"` // matches the command and operation IDs in Orbit
	Action     string    `json:"action"`
	ArgsSHA256 string    `json:"args_sha256"` // args can hold secrets, so only their hash (the signed one) is kept
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

func newActionLog(stateFile string) *actionLog {
	return &actionLog{path: filepath.Join(filepath.Dir(stateFile), "actions.log")}
}

// record appends an entry. Failing to write never blocks a command; the error is returned for logging.
func (l *actionLog) record(cmd protocol.Command, start time.Time, res protocol.Result) error {
	h := sha256.Sum256(cmd.Args)
	b, _ := json.Marshal(actionEntry{start.UTC(), cmd.ID, cmd.Action, hex.EncodeToString(h[:]), res.OK, res.Error,
		time.Since(start).Milliseconds()})
	l.mu.Lock()
	defer l.mu.Unlock()
	if fi, err := os.Stat(l.path); err == nil && fi.Size() > actionLogMax {
		os.Rename(l.path, l.path+".1")
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
