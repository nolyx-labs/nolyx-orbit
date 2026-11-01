// Nomad cluster snapshots and job/install arguments.
package protocol

import (
	"encoding/json"
	"time"
)

type NomadSnapshot struct {
	TS          time.Time       `json:"ts"`
	Region      string          `json:"region"`
	Datacenter  string          `json:"datacenter"`
	Leader      string          `json:"leader"`
	Peers       []string        `json:"peers"`
	Nodes       json.RawMessage `json:"nodes"`
	Jobs        json.RawMessage `json:"jobs"`
	Allocations json.RawMessage `json:"allocations"`
	Deployments json.RawMessage `json:"deployments"`
}

type NomadJobArgs struct {
	JobID string          `json:"job_id"`
	Job   json.RawMessage `json:"job,omitempty"` // register only
}

type NomadInstallArgs struct {
	Server          bool     `json:"server"`
	BootstrapExpect int      `json:"bootstrap_expect"`
	Join            []string `json:"join"`
	Datacenter      string   `json:"datacenter"`
	Region          string   `json:"region"`
	Version         string   `json:"version"`
	AdvertiseIP     string   `json:"advertise_ip"`
}
