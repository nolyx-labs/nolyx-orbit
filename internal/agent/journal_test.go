package agent

import (
	"strings"
	"testing"
)

func TestParseJournal(t *testing.T) {
	in := strings.Join([]string{
		`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1700000000000000","_SYSTEMD_UNIT":"nginx.service","PRIORITY":"3","MESSAGE":"upstream timed out"}`,
		`{"__CURSOR":"c2","__REALTIME_TIMESTAMP":"1700000001000000","_SYSTEMD_UNIT":"init.scope","UNIT":"nginx.service","PRIORITY":"6","MESSAGE":"Started nginx.service"}`,
		`{"__CURSOR":"c3","__REALTIME_TIMESTAMP":"1700000002000000","_SYSTEMD_UNIT":"app.service","PRIORITY":"6","MESSAGE":[104,105,255]}`,
		`{"__CURSOR":"c4","__REALTIME_TIMESTAMP":"1700000003000000","_SYSTEMD_UNIT":"app.service","PRIORITY":"4","MESSAGE":"password=hunter2"}`,
	}, "\n")
	lines, cursor := parseJournal(strings.NewReader(in), 10)
	if len(lines) != 4 || cursor != "c4" {
		t.Fatalf("got %d lines, cursor %s", len(lines), cursor)
	}
	if lines[0].Level != "error" || lines[0].Unit != "nginx.service" || lines[0].TS.Unix() != 1700000000 {
		t.Errorf("line 0: %+v", lines[0])
	}
	if lines[1].Unit != "nginx.service" {
		t.Errorf("systemd's message about a unit belongs to it: %+v", lines[1])
	}
	if !strings.HasPrefix(lines[2].Line, "hi") {
		t.Errorf("byte-array message: %q", lines[2].Line)
	}
	if strings.Contains(lines[3].Line, "hunter2") || lines[3].Level != "warn" {
		t.Errorf("not redacted or wrong level: %+v", lines[3])
	}
	// Stopping early returns the cursor of the last entry read, so the next poll resumes after it.
	if lines, cursor := parseJournal(strings.NewReader(in), 2); len(lines) != 2 || cursor != "c2" {
		t.Errorf("max 2: %d lines, cursor %s", len(lines), cursor)
	}
}
