// Server-wide backups: every Orbit service's volumes (and managed PostgreSQL via pg_dump),
// encrypted on the server and stored in S3 or on another Orbit server over SSH.
package protocol

const (
	ActionServerBackup          = "server.backup"           // archive and upload every service (or one, see Only)
	ActionServerRestore         = "server.restore"          // snapshot one service, then replace its data from a backup
	ActionServerBackupPrune     = "server.backup.prune"     // delete archives that fell out of retention
	ActionServerBackupKey       = "server.backup.key"       // this server's SSH public key, created once; the private key never leaves it
	ActionServerBackupAuthorize = "server.backup.authorize" // on a target server: accept (or revoke) a source server's key
)

func init() {
	for _, a := range []string{ActionServerBackup, ActionServerRestore, ActionServerBackupPrune, ActionServerBackupKey, ActionServerBackupAuthorize} {
		Actions[a] = true
	}
}

// Backup kinds.
const (
	BackupScheduled  = "scheduled"
	BackupManual     = "manual"
	BackupPreDeploy  = "pre-deploy"
	BackupPreRestore = "pre-restore"
)

// SSHTarget is another Orbit server that receives archives as the restricted orbit-backups user.
type SSHTarget struct {
	Addr    string `json:"addr"`     // host:port
	HostKey string `json:"host_key"` // pinned, authorized_keys format
}

// BackupDestination has exactly one of S3 or Server set.
type BackupDestination struct {
	S3     *S3Target  `json:"s3,omitempty"`
	Server *SSHTarget `json:"server,omitempty"`
}

type ServerBackupArgs struct {
	Kind      string            `json:"kind"`
	Dest      BackupDestination `json:"dest"`
	Key       []byte            `json:"key"`                 // 32-byte archive key
	Folder    string            `json:"folder"`              // the server id: S3 keys are <prefix>/<folder>/<file>
	Databases []ServiceDB       `json:"databases,omitempty"` // managed PostgreSQL primaries here: backed up with pg_dump
	Only      string            `json:"only,omitempty"`      // limit to one service, e.g. "app/web"
	ReportID  string            `json:"report_id,omitempty"` // pre-deploy: progress and result are reported under this id
}

type ServiceDB struct {
	Name    string   `json:"name"`
	DB      DBConfig `json:"db"`
	Version string   `json:"version"`
}

// ServiceBackup is one service's archive in a run. Service is "app/<name>" or "db/<name>".
type ServiceBackup struct {
	Service  string `json:"service"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Location string `json:"location"`
	Size     int64  `json:"size"`
	Millis   int64  `json:"millis"`
	Error    string `json:"error,omitempty"`
}

// ServerBackupResult is the output of backups and restores. The agent always reports the archives
// it made, so a failure is in Error (or a service's Error), not in the command result.
type ServerBackupResult struct {
	Backups []ServiceBackup `json:"backups"`
	Error   string          `json:"error,omitempty"`
}

type ServerRestoreArgs struct {
	ServerBackupArgs        // for the pre-restore snapshot; Only is the service to restore
	File             string `json:"file"`
}

type BackupPruneArgs struct {
	Dest   BackupDestination `json:"dest"`
	Folder string            `json:"folder"`
	Files  []string          `json:"files"`
}

type BackupAuthorizeArgs struct {
	SourceID  string `json:"source_id"`
	PublicKey string `json:"public_key"` // empty revokes the source
}

type BackupAuthorizeResult struct {
	HostKey   string `json:"host_key"`
	FreeBytes int64  `json:"free_bytes"`
}
