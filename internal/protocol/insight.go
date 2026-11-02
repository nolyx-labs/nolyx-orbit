// Server insight: systemd services, compose projects, listening ports, specs, security facts, firewall and disk usage.
package protocol

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"
)

const (
	TypeInventory = "inventory" // agent → cp: every few minutes and on connect
	TypeSecurity  = "security"  // agent → cp: on connect and daily

	ActionHostInventory   = "host.inventory"    // returns Inventory
	ActionHostScan        = "host.scan"         // returns SecurityFacts
	ActionHostService     = "host.service"      // ServiceArgs
	ActionHostFirewall    = "host.firewall"     // FirewallArgs; returns SecurityFacts
	ActionHostDisk        = "host.disk"         // DiskArgs; returns DiskUsage
	ActionHostDockerPrune = "host.docker.prune" // returns DockerPruneResult
)

func init() {
	for _, a := range []string{ActionHostInventory, ActionHostScan, ActionHostService, ActionHostFirewall, ActionHostDisk, ActionHostDockerPrune} {
		Actions[a] = true
	}
}

type Inventory struct {
	TS       time.Time        `json:"ts"`
	Services []SystemdService `json:"services"`
	Compose  []ComposeProject `json:"compose"`
	Ports    []ListenPort     `json:"ports"`
	Specs    Specs            `json:"specs"`
	Errors   []string         `json:"errors,omitempty"` // parts that could not be collected, in plain words
}

type SystemdService struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	Load        string `json:"load"`
	Active      string `json:"active"`  // active | inactive | failed | activating ...
	Sub         string `json:"sub"`     // running | exited | dead ...
	Enabled     string `json:"enabled"` // enabled | disabled | static | masked ...
	Memory      uint64 `json:"memory"`  // bytes, 0 when unknown
}

type ComposeProject struct {
	Name       string           `json:"name"`
	WorkingDir string           `json:"working_dir"`
	Services   []ComposeService `json:"services"`
}

type ComposeService struct {
	Service   string `json:"service"`
	Container string `json:"container"`
	State     string `json:"state"`
}

type ListenPort struct {
	Proto     string `json:"proto"` // tcp | udp
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Scope     string `json:"scope"` // all (every interface) | localhost | specific
	PID       int    `json:"pid,omitempty"`
	Process   string `json:"process,omitempty"`
	Container string `json:"container,omitempty"`
}

// Specs complements Hello (OS, kernel, cores, memory, agent version) with what it doesn't carry.
type Specs struct {
	CPUModel   string      `json:"cpu_model"`
	Disks      []Disk      `json:"disks"`
	Interfaces []Interface `json:"interfaces"`
}

type Disk struct {
	Mount  string `json:"mount"`
	Device string `json:"device"`
	FS     string `json:"fs"`
	Size   uint64 `json:"size"`
	Used   uint64 `json:"used"`
}

type Interface struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac"`
	Addrs []string `json:"addrs"`
	Up    bool     `json:"up"`
}

// SecurityFacts are raw observations; the control plane turns them into findings so wording can change without an agent upgrade.
type SecurityFacts struct {
	TS              time.Time    `json:"ts"`
	Container       bool         `json:"container"` // agent runs in a container: host-only checks were skipped
	Ports           []ListenPort `json:"ports"`
	Firewall        Firewall     `json:"firewall"`
	SSH             SSHConfig    `json:"ssh"`
	AutoUpdates     string       `json:"auto_updates"` // tool name when configured, "" when not, "unknown"
	RebootRequired  bool         `json:"reboot_required"`
	SecurityUpdates int          `json:"security_updates"` // -1 when it couldn't be determined cheaply
	Errors          []string     `json:"errors,omitempty"`
}

type SSHConfig struct {
	Installed       bool   `json:"installed"`
	PermitRootLogin string `json:"permit_root_login"`
	PasswordAuth    string `json:"password_authentication"`
	Ports           []int  `json:"ports"`
}

type Firewall struct {
	Tool     string         `json:"tool"` // ufw | firewalld | "" (none found)
	Active   bool           `json:"active"`
	Incoming string         `json:"incoming"` // default policy: deny | reject | allow
	Outgoing string         `json:"outgoing"`
	Rules    []FirewallRule `json:"rules"`
}

type FirewallRule struct {
	Port   string `json:"port"`  // "22", "80,443", "8000:8100" or a service name such as "OpenSSH"
	Proto  string `json:"proto"` // tcp | udp | "" (both)
	Action string `json:"action"`
	From   string `json:"from"` // "Anywhere" or a CIDR
	V6     bool   `json:"v6,omitempty"`
	Raw    string `json:"raw"`
}

type ServiceArgs struct {
	Unit   string `json:"unit"`
	Action string `json:"action"` // start | stop | restart | reload
}

type FirewallArgs struct {
	Action string `json:"action"` // open | close
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
	Source string `json:"source,omitempty"` // CIDR or IP; empty means anywhere
}

type DiskArgs struct {
	Path string `json:"path"`
}

type DiskUsage struct {
	Path    string     `json:"path"`
	Dirs    []DirUsage `json:"dirs"`
	Partial bool       `json:"partial"` // du hit its time limit
	Note    string     `json:"note,omitempty"`
	Docker  *DockerDF  `json:"docker,omitempty"`
}

type DirUsage struct {
	Path string `json:"path"`
	Size uint64 `json:"size"`
}

type DockerDF struct {
	Images     DFLine `json:"images"`
	Containers DFLine `json:"containers"`
	Volumes    DFLine `json:"volumes"`
	BuildCache DFLine `json:"build_cache"`
}

type DFLine struct {
	Count       int    `json:"count"`
	Size        uint64 `json:"size"`
	Reclaimable uint64 `json:"reclaimable"`
}

type DockerPruneResult struct {
	Reclaimed uint64 `json:"reclaimed"`
}

var serviceActions = map[string]bool{"start": true, "stop": true, "restart": true, "reload": true}

// unitName rejects anything systemctl could read as an option or a glob.
var unitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._:\\-]{0,200}\.service$`)

func ValidUnit(u string) bool { return unitName.MatchString(u) }

func (a ServiceArgs) Validate() error {
	if !unitName.MatchString(a.Unit) {
		return fmt.Errorf("invalid unit name %q", a.Unit)
	}
	if !serviceActions[a.Action] {
		return fmt.Errorf("invalid action %q", a.Action)
	}
	return nil
}

// Validate normalizes Source to CIDR form so the agent never sees free text.
func (a *FirewallArgs) Validate() error {
	if a.Action != "open" && a.Action != "close" {
		return fmt.Errorf("invalid action %q", a.Action)
	}
	if a.Port < 1 || a.Port > 65535 {
		return fmt.Errorf("port must be 1–65535")
	}
	if a.Proto != "tcp" && a.Proto != "udp" {
		return fmt.Errorf("protocol must be tcp or udp")
	}
	if a.Source == "" {
		return nil
	}
	if ip := net.ParseIP(a.Source); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		a.Source = ip.String() + "/" + strconv.Itoa(bits)
		return nil
	}
	_, n, err := net.ParseCIDR(a.Source)
	if err != nil {
		return fmt.Errorf("source must be an IP address or CIDR, like 203.0.113.0/24")
	}
	a.Source = n.String()
	return nil
}
