// Host facts, metrics and container snapshots reported by the agent.
package protocol

import "time"

type Hello struct {
	Hostname      string `json:"hostname"`
	IP            string `json:"ip"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Kernel        string `json:"kernel"`
	CPUCount      int    `json:"cpu_count"`
	MemTotal      uint64 `json:"mem_total"`
	DiskTotal     uint64 `json:"disk_total"`
	AgentVersion  string `json:"agent_version"`
	DockerVersion string `json:"docker_version"`
	NomadVersion  string `json:"nomad_version"`
	Region        string `json:"region"`
}

type Metrics struct {
	TS        time.Time `json:"ts"`
	CPU       float64   `json:"cpu"` // percent of all cores
	MemUsed   uint64    `json:"mem_used"`
	MemTotal  uint64    `json:"mem_total"`
	DiskUsed  uint64    `json:"disk_used"`
	DiskTotal uint64    `json:"disk_total"`
	NetRx     uint64    `json:"net_rx"` // bytes/sec
	NetTx     uint64    `json:"net_tx"`
	Load1     float64   `json:"load1"`
	Uptime    uint64    `json:"uptime"`   // seconds
	IORead    uint64    `json:"io_read"`  // bytes/sec, whole disks
	IOWrite   uint64    `json:"io_write"` // bytes/sec
	IOUtil    float64   `json:"io_util"`  // percent of time the busiest disk was doing I/O
}

// MetricsBatch lets the agent flush samples buffered while disconnected.
type MetricsBatch struct {
	Samples []Metrics `json:"samples"`
}

type Container struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	Health       string            `json:"health"`
	Created      time.Time         `json:"created"`
	Ports        []string          `json:"ports"`
	Labels       map[string]string `json:"labels"`
	RestartCount int               `json:"restart_count"`
	CPU          float64           `json:"cpu"`
	MemUsed      uint64            `json:"mem_used"`
	MemLimit     uint64            `json:"mem_limit"`
}

type ContainerSnapshot struct {
	TS         time.Time   `json:"ts"`
	Containers []Container `json:"containers"`
}
