// Signed commands, their arguments, progress and results.
package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Command struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Args      json.RawMessage `json:"args"`
	ExpiresAt time.Time       `json:"expires_at"`
	Signature []byte          `json:"signature"` // ed25519 over SigningPayload
}

// SigningPayload binds a command to one server, so a captured command can't be replayed elsewhere.
func SigningPayload(serverID string, c Command) []byte {
	h := sha256.Sum256(c.Args)
	return []byte(c.ID + "\n" + serverID + "\n" + c.Action + "\n" + c.ExpiresAt.UTC().Format(time.RFC3339Nano) + "\n" + hex.EncodeToString(h[:]))
}

type ContainerArgs struct {
	ContainerID string   `json:"container_id"`
	Tail        int      `json:"tail,omitempty"`
	Cmd         []string `json:"cmd,omitempty"`     // exec
	Timeout     int      `json:"timeout,omitempty"` // exec, seconds
}

type Progress struct {
	ID     string `json:"id"`
	Step   string `json:"step"`
	Status string `json:"status"` // running | done | failed
	Line   string `json:"line,omitempty"`
}

type Result struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
}

type DeployArgs struct {
	App        string            `json:"app"`
	Image      string            `json:"image"`
	Ports      []string          `json:"ports"` // "host:container"
	Env        map[string]string `json:"env"`
	HealthPath string            `json:"health_path"`
	Proxy      bool              `json:"proxy"` // zero-downtime: the agent owns the first host port and switches upstreams
	Build      *BuildArgs        `json:"build,omitempty"`
	CPUs       float64           `json:"cpus,omitempty"`
	MemoryMB   int               `json:"memory_mb,omitempty"`
	Snapshot   *ServerBackupArgs `json:"snapshot,omitempty"`   // pre-deploy volume snapshot
	Volumes    []string          `json:"volumes,omitempty"`    // "name:/path", mounted from the Docker volume VolumeName(App, name)
	Command    []string          `json:"command,omitempty"`    // replaces the image's CMD
	HealthCmd  string            `json:"health_cmd,omitempty"` // Docker HEALTHCHECK (shell); the deploy waits for "healthy"
}

// BuildArgs builds Image from a git repository with the Docker daemon's remote-context build.
type BuildArgs struct {
	Remote     string `json:"remote"` // https URL, may embed an access token
	Ref        string `json:"ref"`    // commit sha or branch
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
}

// DeployPlan lists the steps a deploy will report, so the UI can render pending ones.
func DeployPlan(a DeployArgs) []string {
	first := "Pulling image"
	if a.Build != nil {
		first = "Building image"
	}
	if a.Proxy {
		return []string{first, "Creating container", "Starting container", "Health check", "Switching traffic", "Cleaning up"}
	}
	return []string{first, "Stopping previous", "Creating container", "Starting container", "Health check", "Cleaning up"}
}

// ParsePort accepts "3000", "8080:3000" and "8080:3000/udp", returning the host port and "3000/tcp".
func ParsePort(p string) (host, ctr string, err error) {
	proto := "tcp"
	if i := strings.Index(p, "/"); i >= 0 {
		p, proto = p[:i], p[i+1:]
	}
	parts := strings.Split(p, ":")
	switch len(parts) {
	case 1:
		host, ctr = parts[0], parts[0]
	case 2:
		host, ctr = parts[0], parts[1]
	default:
		return "", "", fmt.Errorf("invalid port %q", p)
	}
	for _, n := range []string{host, ctr} {
		if v, err := strconv.Atoi(n); err != nil || v < 1 || v > 65535 {
			return "", "", fmt.Errorf("invalid port %q", p)
		}
	}
	if proto != "tcp" && proto != "udp" {
		return "", "", fmt.Errorf("invalid port protocol %q", proto)
	}
	return host, ctr + "/" + proto, nil
}

var volumeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// ParseVolume accepts "data:/var/lib/mysql": a service-local volume name and an absolute container path.
func ParseVolume(v string) (name, path string, err error) {
	name, path, ok := strings.Cut(v, ":")
	if !ok || !volumeNameRe.MatchString(name) || !strings.HasPrefix(path, "/") || path == "/" ||
		strings.Contains(path, "..") || strings.ContainsAny(path, ":,\x00\n") {
		return "", "", fmt.Errorf("invalid volume %q (want name:/container/path)", v)
	}
	return name, path, nil
}

// VolumeName is the Docker volume backing a service's named volume; it outlives containers and redeploys.
func VolumeName(app, name string) string { return "orbit-" + app + "-" + name }
