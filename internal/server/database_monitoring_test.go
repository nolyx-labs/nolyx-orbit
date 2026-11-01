package server

import (
	"orbit/internal/protocol"
	"testing"
)

func TestDBIssues(t *testing.T) {
	lag := 4800.0
	issues := dbIssues(protocol.DBStats{OK: true, Role: "replica", LagMs: &lag, Receiver: &protocol.WalReceiver{Status: "streaming"}, Connections: 10, MaxConnections: 100})
	if len(issues) != 1 || issues[0] != "Replication lag 4.8s" {
		t.Fatalf("issues = %v", issues)
	}
	if got := dbIssues(protocol.DBStats{OK: true, Role: "primary", Connections: 95, MaxConnections: 100}); len(got) != 1 {
		t.Fatalf("connection pressure not reported: %v", got)
	}
}
