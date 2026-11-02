// Server insight: periodic inventory and security reports, and the host.* commands.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"orbit/internal/protocol"
)

var errContainerMode = errors.New("not available when the agent runs in a container: install it with install.sh to manage the host")

// run executes a host tool with an argument list (never a shell), with stable C-locale output.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(strings.TrimSpace(string(ee.Stderr))) > 0 {
		err = fmt.Errorf("%s: %s", name, tail(strings.TrimSpace(string(ee.Stderr)), 3))
	}
	return string(out), err
}

func has(tool string) bool { _, err := exec.LookPath(tool); return err == nil }

// reportInsight sends the inventory every 5 minutes and a security report daily, both right after each (re)connect.
func (a *Agent) reportInsight(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	var last *websocket.Conn
	var invAt, scanAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		c := a.conn
		a.mu.Unlock()
		if c == nil {
			continue
		}
		fresh := c != last
		last = c
		if fresh || time.Since(invAt) > 5*time.Minute {
			ictx, cancel := context.WithTimeout(ctx, time.Minute)
			if a.sendNow(ictx, protocol.TypeInventory, a.inventory(ictx)) {
				invAt = time.Now()
			}
			cancel()
		}
		// A flapping connection shouldn't rerun package-manager queries every few seconds.
		if (fresh && time.Since(scanAt) > 10*time.Minute) || time.Since(scanAt) > 24*time.Hour {
			sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if a.sendNow(sctx, protocol.TypeSecurity, a.securityFacts(sctx)) {
				scanAt = time.Now()
			}
			cancel()
		}
	}
}

func (a *Agent) inventory(ctx context.Context) protocol.Inventory {
	inv := protocol.Inventory{TS: time.Now().UTC(), Services: []protocol.SystemdService{}, Compose: []protocol.ComposeProject{}}
	if a.cfg.HostRoot != "" {
		inv.Errors = append(inv.Errors, "System services: "+errContainerMode.Error())
	} else if svcs, err := listServices(ctx); err != nil {
		inv.Errors = append(inv.Errors, "System services: "+err.Error())
	} else {
		inv.Services = svcs
	}
	cs, _ := a.docker.list(ctx, "") // Docker is optional
	inv.Compose = composeProjects(cs)
	inv.Ports = a.host.Ports(cs)
	inv.Specs = a.host.Specs()
	return inv
}

// hostCommand runs the host.* actions. Each finishes well inside the control plane's 90s wait.
func (a *Agent) hostCommand(ctx context.Context, cmd protocol.Command) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	switch cmd.Action {
	case protocol.ActionHostInventory:
		return a.inventory(ctx), nil
	case protocol.ActionHostScan:
		return a.securityFacts(ctx), nil
	case protocol.ActionHostService:
		var args protocol.ServiceArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return nil, a.serviceAction(ctx, args)
	case protocol.ActionHostFirewall:
		var args protocol.FirewallArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		if err := a.firewallChange(ctx, args); err != nil {
			return nil, err
		}
		return a.securityFacts(ctx), nil
	case protocol.ActionHostDisk:
		var args protocol.DiskArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return a.diskUsage(ctx, args)
	case protocol.ActionHostDockerPrune:
		return a.docker.prune(ctx)
	}
	return nil, fmt.Errorf("unknown action %q", cmd.Action)
}

func composeProjects(cs []dockerContainer) []protocol.ComposeProject {
	byName := map[string]*protocol.ComposeProject{}
	for _, c := range cs {
		name := c.Labels["com.docker.compose.project"]
		if name == "" {
			continue
		}
		p := byName[name]
		if p == nil {
			p = &protocol.ComposeProject{Name: name, WorkingDir: c.Labels["com.docker.compose.project.working_dir"]}
			byName[name] = p
		}
		ctr := ""
		if len(c.Names) > 0 {
			ctr = strings.TrimPrefix(c.Names[0], "/")
		}
		p.Services = append(p.Services, protocol.ComposeService{Service: c.Labels["com.docker.compose.service"], Container: ctr, State: c.State})
	}
	out := []protocol.ComposeProject{}
	for _, p := range byName {
		sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Service < p.Services[j].Service })
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Specs reports what Hello doesn't: CPU model, filesystems and network interfaces.
func (h *Host) Specs() protocol.Specs {
	var sp protocol.Specs
	for _, line := range strings.Split(h.read("/proc/cpuinfo"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if k = strings.TrimSpace(k); ok && sp.CPUModel == "" && (k == "model name" || k == "Model" || k == "Hardware") {
			sp.CPUModel = strings.TrimSpace(v)
		}
	}
	sp.Disks = parseMounts(h.read("/proc/1/mounts"))
	for i := range sp.Disks {
		var st syscall.Statfs_t
		if syscall.Statfs(h.Root+sp.Disks[i].Mount, &st) == nil {
			bs := uint64(st.Bsize)
			sp.Disks[i].Size, sp.Disks[i].Used = st.Blocks*bs, (st.Blocks-st.Bfree)*bs
		}
	}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || strings.HasPrefix(ifc.Name, "veth") {
			continue
		}
		x := protocol.Interface{Name: ifc.Name, MAC: ifc.HardwareAddr.String(), Up: ifc.Flags&net.FlagUp != 0, Addrs: []string{}}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			x.Addrs = append(x.Addrs, ad.String())
		}
		sp.Interfaces = append(sp.Interfaces, x)
	}
	return sp
}

// parseMounts keeps real block-device filesystems, one entry per device (bind mounts repeat them).
func parseMounts(data string) []protocol.Disk {
	skip := map[string]bool{"squashfs": true, "iso9660": true, "overlay": true}
	seen := map[string]bool{}
	out := []protocol.Disk{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "/dev/") || strings.HasPrefix(f[0], "/dev/loop") || skip[f[2]] || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		out = append(out, protocol.Disk{Device: f[0], Mount: strings.ReplaceAll(f[1], `\040`, " "), FS: f[2]})
	}
	return out
}
