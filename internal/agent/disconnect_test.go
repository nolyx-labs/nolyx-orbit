package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisconnect(t *testing.T) {
	for _, target := range []bool{false, true} {
		root := t.TempDir()
		files := []string{"/etc/systemd/system/orbit-agent.service", "/etc/orbit-agent/env", "/var/lib/orbit-agent/state.json",
			"/var/lib/orbit-agent/actions.log", "/usr/local/bin/orbit-agent"}
		if target {
			files = append(files, receiveRoot+"/src/a.tar")
		}
		for _, f := range files {
			os.MkdirAll(filepath.Dir(root+f), 0o700)
			os.WriteFile(root+f, []byte("x"), 0o600)
		}
		var ran []string
		var out strings.Builder
		code := disconnect(root, "/var/lib/orbit-agent/state.json", func(a ...string) error { ran = append(ran, strings.Join(a, " ")); return nil }, &out)
		if code != 0 || ran[0] != "systemctl disable --now orbit-agent" {
			t.Fatalf("code %d, ran %v\n%s", code, ran, out.String())
		}
		exists := func(f string) bool { _, err := os.Stat(root + f); return err == nil }
		for _, f := range []string{"/etc/systemd/system/orbit-agent.service", "/etc/orbit-agent", "/var/lib/orbit-agent/state.json"} {
			if exists(f) {
				t.Errorf("%s kept", f)
			}
		}
		if !exists("/var/lib/orbit-agent/actions.log") {
			t.Error("action log removed")
		}
		if exists("/usr/local/bin/orbit-agent") != target {
			t.Errorf("backup target %v: binary kept = %v", target, !target)
		}
	}
}
