// Security findings derived from the facts an agent reports.
package server

import (
	"fmt"
	"orbit/internal/protocol"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type finding struct {
	Key      string `json:"key"`      // stable across scans, used to spot new findings
	Severity string `json:"severity"` // high | medium | low
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

var riskyPorts = map[int]string{
	2375: "Docker API", 2379: "etcd", 3306: "MySQL", 5432: "PostgreSQL", 5984: "CouchDB",
	6379: "Redis", 9200: "Elasticsearch", 11211: "Memcached", 27017: "MongoDB",
}

var severityRank = map[string]int{"high": 0, "medium": 1, "low": 2}

func securityFindings(f protocol.SecurityFacts) []finding {
	out := []finding{}
	add := func(key, sev, title, detail, fix string) { out = append(out, finding{key, sev, title, detail, fix}) }
	fw := f.Firewall
	switch {
	case f.Container:
	case fw.Tool == "":
		add("firewall.missing", "medium", "No firewall installed",
			"Nothing on this server filters incoming connections, so every port listening on all interfaces is reachable by anyone who can reach the server. A firewall at your cloud provider also protects it, but Orbit can't see that one.",
			"Install ufw, allow SSH, then turn it on: apt install ufw && ufw allow OpenSSH && ufw enable")
	case !fw.Active && fw.Tool == "ufw":
		add("firewall.inactive", "medium", "Firewall is turned off", "ufw is installed but not active, so it doesn't block anything.",
			"Allow SSH first, then turn it on: ufw allow OpenSSH && ufw enable")
	case !fw.Active:
		add("firewall.inactive", "medium", "Firewall is turned off", "firewalld is installed but not running, so it doesn't block anything.",
			"systemctl enable --now firewalld")
	}

	sshPorts := f.SSH.Ports
	if len(sshPorts) == 0 {
		sshPorts = []int{22}
	}
	seen := map[string]bool{}
	for _, p := range f.Ports {
		key := fmt.Sprintf("port.%s.%d", p.Proto, p.Port)
		if p.Scope != "all" || seen[key] || (p.Proto == "tcp" && slices.Contains(sshPorts, p.Port)) {
			continue
		}
		seen[key] = true
		docker := p.Process == "docker-proxy"
		if !docker && fw.Active && fw.Incoming != "allow" && !openToAnyone(fw, p) {
			continue
		}
		label := fmt.Sprintf("%d/%s", p.Port, p.Proto)
		if name := riskyPorts[p.Port]; name != "" {
			label += " (" + name + ")"
		}
		owner := p.Container
		if owner == "" {
			owner = p.Process
		}
		if owner == "" {
			owner = "A process"
		}
		sev := "medium"
		switch {
		case riskyPorts[p.Port] != "":
			sev = "high"
		case p.Port == 80 || p.Port == 443:
			sev = "low"
		}
		title, detail := "Port "+label+" is reachable from outside", owner+" listens on all network interfaces and the firewall doesn't block it."
		fix := "If nothing outside this server needs it, make it listen on 127.0.0.1 only, or block the port in the firewall."
		switch {
		case docker:
			detail = fmt.Sprintf("Docker publishes this port for %s. Docker's own rules bypass ufw and firewalld, so it is reachable even if the firewall blocks the port.", owner)
			fix = "Publish it on 127.0.0.1 only (for example 127.0.0.1:" + strconv.Itoa(p.Port) + ":" + strconv.Itoa(p.Port) + "), or remove the port mapping if nothing outside needs it."
		case f.Container:
			title, detail = "Port "+label+" may be reachable from outside", owner+" listens on all network interfaces. The firewall couldn't be checked because the agent runs in a container."
		}
		add(key, sev, title, detail, fix)
	}

	if f.SSH.PermitRootLogin == "yes" {
		add("ssh.root_login", "high", "Root can log in over SSH with a password",
			"PermitRootLogin is yes, so anyone who guesses the root password gets full control of the server.",
			"Set PermitRootLogin prohibit-password (keys only) or no in /etc/ssh/sshd_config, then run systemctl reload ssh")
	}
	if f.SSH.PasswordAuth == "yes" {
		add("ssh.password_auth", "medium", "SSH accepts passwords",
			"Password logins are open to brute-force attempts. SSH keys are much harder to guess.",
			"Make sure your SSH key works, set PasswordAuthentication no in /etc/ssh/sshd_config, then run systemctl reload ssh")
	}
	if f.AutoUpdates == "" {
		add("updates.automatic", "medium", "Automatic security updates are off",
			"Security fixes are only installed when someone remembers to do it.",
			"Debian/Ubuntu: apt install unattended-upgrades && dpkg-reconfigure -plow unattended-upgrades. RHEL/Fedora: dnf install dnf-automatic && systemctl enable --now dnf-automatic-install.timer")
	}
	if f.SecurityUpdates > 0 {
		add("updates.pending", "medium", fmt.Sprintf("%d security update%s waiting", f.SecurityUpdates, map[bool]string{true: "", false: "s"}[f.SecurityUpdates == 1]),
			"A quick count from the server's package cache, which may be up to a day old. It doesn't install or manage updates.",
			"Install them: apt upgrade (Debian/Ubuntu) or dnf upgrade --security (RHEL/Fedora)")
	}
	if f.RebootRequired {
		add("reboot.required", "low", "Restart needed",
			"Updates were installed that only take effect after a restart, usually a new kernel.",
			"Restart the server at a quiet time.")
	}
	sort.SliceStable(out, func(i, j int) bool { return severityRank[out[i].Severity] < severityRank[out[j].Severity] })
	return out
}

// openToAnyone reports whether an inbound allow rule from anywhere covers the port.
// Rules limited to a source range don't count: the port isn't reachable from the internet at large.
func openToAnyone(fw protocol.Firewall, p protocol.ListenPort) bool {
	for _, r := range fw.Rules {
		a := strings.ToUpper(r.Action)
		anywhere := r.From == "Anywhere" || r.From == "0.0.0.0/0" || r.From == "::/0"
		if (strings.HasPrefix(a, "ALLOW") || strings.HasPrefix(a, "LIMIT")) && !strings.Contains(a, "OUT") && !strings.Contains(a, "FWD") &&
			anywhere && (r.Proto == "" || r.Proto == p.Proto) && portMatches(r.Port, p.Port) {
			return true
		}
	}
	return false
}

// portMatches understands "22", "80,443", "8000:8100", "Anywhere" and a few service names.
// shortcut: other ufw app profiles and firewalld services are treated as not matching.
func portMatches(spec string, port int) bool {
	spec = strings.ToLower(spec)
	if spec == "anywhere" {
		return true
	}
	if named := map[string]string{"ssh": "22", "openssh": "22", "http": "80", "https": "443", "nginx full": "80,443", "apache full": "80,443"}[spec]; named != "" {
		spec = named
	}
	for _, part := range strings.Split(spec, ",") {
		lo, hi, isRange := strings.Cut(part, ":")
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if !isRange {
			b, err2 = a, err1
		}
		if err1 == nil && err2 == nil && port >= a && port <= b {
			return true
		}
	}
	return false
}
