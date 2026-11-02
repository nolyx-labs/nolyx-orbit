// Security facts: SSH settings, automatic updates, pending security updates, reboot required.
package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orbit/internal/protocol"
)

func (a *Agent) securityFacts(ctx context.Context) protocol.SecurityFacts {
	f := protocol.SecurityFacts{TS: time.Now().UTC(), Container: a.cfg.HostRoot != "", SecurityUpdates: -1}
	cs, _ := a.docker.list(ctx, "")
	f.Ports = a.host.Ports(cs)
	f.AutoUpdates = autoUpdates(a.cfg.HostRoot)
	_, err := os.Stat(a.cfg.HostRoot + "/run/reboot-required")
	f.RebootRequired = err == nil
	f.Firewall.Rules = []protocol.FirewallRule{}
	if f.Container {
		return f // firewall, sshd and package managers live on the host
	}
	if f.Firewall, err = readFirewall(ctx); err != nil {
		f.Errors = append(f.Errors, "Firewall: "+err.Error())
	}
	f.SSH = sshConfig(ctx)
	f.SecurityUpdates = securityUpdates(ctx)
	if !f.RebootRequired && has("needs-restarting") {
		var ee *exec.ExitError
		_, err := run(ctx, "needs-restarting", "-r")
		f.RebootRequired = errors.As(err, &ee) && ee.ExitCode() == 1
	}
	return f
}

func sshConfig(ctx context.Context) protocol.SSHConfig {
	if !has("sshd") {
		return protocol.SSHConfig{}
	}
	out, _ := run(ctx, "sshd", "-T")
	return parseSSHD(out)
}

func parseSSHD(out string) protocol.SSHConfig {
	c := protocol.SSHConfig{Installed: true, Ports: []int{}}
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch k {
		case "permitrootlogin":
			c.PermitRootLogin = v
		case "passwordauthentication":
			c.PasswordAuth = v
		case "port":
			if n, err := strconv.Atoi(v); err == nil {
				c.Ports = append(c.Ports, n)
			}
		}
	}
	return c
}

// autoUpdates checks configuration files only, so it also works from the agent container via the host root.
func autoUpdates(root string) string {
	if files, _ := filepath.Glob(root + "/etc/apt/apt.conf.d/*"); len(files) > 0 {
		if _, err := os.Stat(root + "/usr/bin/unattended-upgrade"); err == nil {
			for _, f := range files {
				b, _ := os.ReadFile(f)
				if strings.Contains(string(b), `APT::Periodic::Unattended-Upgrade "1"`) {
					return "unattended-upgrades"
				}
			}
		}
		return ""
	}
	if timers, _ := filepath.Glob(root + "/etc/systemd/system/timers.target.wants/dnf-automatic*.timer"); len(timers) > 0 {
		return "dnf-automatic"
	}
	if _, err := os.Stat(root + "/usr/bin/dnf"); err == nil {
		return ""
	}
	return "unknown"
}

// securityUpdates is a cheap count from the local package cache. Full update management is a separate feature.
func securityUpdates(ctx context.Context) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch {
	case has("apt-get"):
		out, err := run(ctx, "apt-get", "-s", "-o", "Debug::NoLocking=1", "upgrade")
		if err == nil {
			return countAptSecurity(out)
		}
	case has("dnf"):
		out, err := run(ctx, "dnf", "-q", "--cacheonly", "updateinfo", "list", "--security")
		if s := strings.TrimSpace(out); err == nil && s == "" {
			return 0
		} else if err == nil {
			return strings.Count(s, "\n") + 1 // one advisory per line
		}
	}
	return -1
}

func countAptSecurity(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Inst ") && strings.Contains(line, "-security") {
			n++
		}
	}
	return n
}
