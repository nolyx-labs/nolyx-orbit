// Package protocol defines the wire format between orbit-agent and the control plane (v1).
package protocol

import (
	"encoding/json"
)

const Version = "1"

// Frame types.
const (
	TypeHello      = "hello"
	TypeMetrics    = "metrics"
	TypeContainers = "containers"
	TypeCommand    = "command"
	TypeProgress   = "progress"
	TypeResult     = "result"
	TypeConfig     = "config"      // cp → agent: monitoring targets
	TypeDBStats    = "db_stats"    // agent → cp
	TypeLogs       = "logs"        // agent → cp
	TypeAppMetrics = "app_metrics" // agent → cp: proxy request metrics
	TypeNomad      = "nomad"       // agent → cp: cluster snapshot
)

// Command actions. The agent rejects anything not in this list.
const (
	ActionContainerStart   = "container.start"
	ActionContainerStop    = "container.stop"
	ActionContainerRestart = "container.restart"
	ActionContainerRemove  = "container.remove"
	ActionContainerLogs    = "container.logs"
	ActionContainerExec    = "container.exec" // workflows: run a command, capture its output
	ActionDeploy           = "deploy"
	ActionDBProvision      = "db.provision"      // managed PostgreSQL primary
	ActionDBReplicaCreate  = "db.replica.create" // pg_basebackup from a primary
	ActionDBResync         = "db.resync"         // wipe and re-clone a managed replica
	ActionDBRemove         = "db.remove"         // remove a managed container and its volume
	ActionDBPromote        = "db.promote"
	ActionDBBackup         = "db.backup"
	ActionDBRestore        = "db.restore"
	ActionNomadRegister    = "nomad.register"
	ActionNomadStop        = "nomad.stop"
	ActionNomadInstall     = "nomad.install"
	ActionOSCheck          = "os.check"   // list pending package updates
	ActionOSInstall        = "os.install" // install updates, optionally rebooting
	ActionOSHold           = "os.hold"    // hold or release a package
	ActionOSReboot         = "os.reboot"
)

var Actions = map[string]bool{
	ActionContainerStart: true, ActionContainerStop: true, ActionContainerRestart: true,
	ActionContainerRemove: true, ActionContainerLogs: true, ActionContainerExec: true, ActionDeploy: true,
	ActionDBProvision: true, ActionDBReplicaCreate: true, ActionDBResync: true, ActionDBRemove: true,
	ActionDBPromote: true, ActionDBBackup: true, ActionDBRestore: true,
	ActionNomadRegister: true, ActionNomadStop: true, ActionNomadInstall: true,
	ActionOSCheck: true, ActionOSInstall: true, ActionOSHold: true, ActionOSReboot: true,
}

type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func Encode(typ string, payload any) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: typ, Payload: p})
}

type EnrollRequest struct {
	Token string `json:"token"`
	Host  Hello  `json:"host"`
}

type EnrollResponse struct {
	ServerID   string `json:"server_id"`
	Secret     string `json:"secret"`
	SigningKey []byte `json:"signing_key"` // ed25519 public key the agent pins
}
