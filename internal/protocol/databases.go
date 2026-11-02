// Database monitoring, managed PostgreSQL and backup messages.
package protocol

import "time"

type DBConfig struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

type AgentConfig struct {
	Databases []DBConfig      `json:"databases"`
	LogUnits  []string        `json:"log_units,omitempty"` // systemd services whose journal is shipped with the container logs
	Monitors  []MonitorConfig `json:"monitors,omitempty"`  // uptime checks this server runs as an extra location
}

type SlowQuery struct {
	PID      int     `json:"pid"`
	Seconds  float64 `json:"seconds"`
	State    string  `json:"state"`
	Query    string  `json:"query"`
	WaitType string  `json:"wait"`
}

type TableSize struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Rows  int64  `json:"rows"`
}

type ReplicaInfo struct {
	Name        string   `json:"name"`
	ClientAddr  string   `json:"client_addr"`
	State       string   `json:"state"`
	SyncState   string   `json:"sync_state"`
	ReplayLSN   string   `json:"replay_lsn"`
	LagBytes    int64    `json:"lag_bytes"`
	ReplayLagMs *float64 `json:"replay_lag_ms"`
}

type WalReceiver struct {
	Status     string `json:"status"`
	SenderHost string `json:"sender_host"`
	SenderPort int    `json:"sender_port"`
}

type DBStats struct {
	ID             string        `json:"id"`
	TS             time.Time     `json:"ts"`
	OK             bool          `json:"ok"`
	Error          string        `json:"error,omitempty"`
	Version        string        `json:"version"`
	SystemID       string        `json:"system_id"`
	Role           string        `json:"role"` // primary | replica
	Connections    int           `json:"connections"`
	MaxConnections int           `json:"max_connections"`
	TPS            *float64      `json:"tps"`
	QPS            *float64      `json:"qps"` // needs pg_stat_statements
	CacheHit       *float64      `json:"cache_hit"`
	Size           int64         `json:"size"`
	WaitingLocks   int           `json:"waiting_locks"`
	SlowQueries    []SlowQuery   `json:"slow_queries"`
	Tables         []TableSize   `json:"tables"`
	WALPosition    string        `json:"wal_position"`
	LagMs          *float64      `json:"lag_ms"`      // replica: replay delay
	ReplayRate     *float64      `json:"replay_rate"` // replica: bytes/sec replayed
	Receiver       *WalReceiver  `json:"receiver,omitempty"`
	Replicas       []ReplicaInfo `json:"replicas"`
}

// PGArgs drives managed PostgreSQL containers (primary, replica, resync, remove).
type PGArgs struct {
	Name         string  `json:"name"` // container orbit-pg-<name>, volume orbit-pg-<name>-data
	Version      string  `json:"version"`
	Port         int     `json:"port"`
	Password     string  `json:"password"`      // superuser (postgres)
	ReplPassword string  `json:"repl_password"` // orbit_replicator
	DBName       string  `json:"dbname"`
	PrimaryHost  string  `json:"primary_host,omitempty"`
	PrimaryPort  int     `json:"primary_port,omitempty"`
	CPUs         float64 `json:"cpus,omitempty"`
	MemoryMB     int     `json:"memory_mb,omitempty"`
}

type DBArgs struct {
	DatabaseID string `json:"database_id"`
}

type S3Target struct {
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	Region    string `json:"region"`
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// BackupArgs is used for both backup and restore of a pg_dump custom-format file.
type BackupArgs struct {
	DB      DBConfig  `json:"db"`
	Version string    `json:"version"` // client image major version
	File    string    `json:"file"`
	S3      *S3Target `json:"s3,omitempty"`
	Prune   []string  `json:"prune,omitempty"` // backup: older files to delete after success (retention)
}
