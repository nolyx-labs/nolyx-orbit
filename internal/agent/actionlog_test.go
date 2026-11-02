package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orbit/internal/protocol"
)

func TestActionLog(t *testing.T) {
	dir := t.TempDir()
	l := newActionLog(filepath.Join(dir, "state.json"))
	cmd := protocol.Command{ID: "c1", Action: protocol.ActionDeploy, Args: json.RawMessage(`{"env":{"SECRET":"hunter2"}}`)}
	if err := l.record(cmd, time.Now(), protocol.Result{ID: "c1", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "actions.log"))
	var e actionEntry
	if err := json.Unmarshal(b, &e); err != nil || e.ID != "c1" || e.Action != protocol.ActionDeploy || e.OK || e.Error != "boom" || len(e.ArgsSHA256) != 64 {
		t.Fatalf("entry %s (%v)", b, err)
	}
	if strings.Contains(string(b), "hunter2") {
		t.Error("args leaked into the action log")
	}
	if fi, _ := os.Stat(filepath.Join(dir, "actions.log")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}

	os.WriteFile(l.path, make([]byte, actionLogMax+1), 0o600)
	l.record(cmd, time.Now(), protocol.Result{OK: true})
	if _, err := os.Stat(l.path + ".1"); err != nil {
		t.Error("not rotated")
	}
}
