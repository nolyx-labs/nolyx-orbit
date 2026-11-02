// systemd services: discovery and start/stop/restart/reload.
package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"orbit/internal/protocol"
)

const agentUnit = "orbit-agent.service"

// lockoutUnits are refused "stop": losing them cuts off SSH or the network the agent needs.
var lockoutUnits = map[string]bool{
	"ssh.service": true, "sshd.service": true, agentUnit: true,
	"systemd-networkd.service": true, "NetworkManager.service": true, "networking.service": true,
}

func listServices(ctx context.Context) ([]protocol.SystemdService, error) {
	out, err := run(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager")
	if err != nil {
		return nil, err
	}
	svcs := parseListUnits(out)
	if len(svcs) == 0 {
		return svcs, nil
	}
	args := []string{"show", "--no-pager", "--property=Id,UnitFileState,MemoryCurrent", "--"}
	for _, s := range svcs {
		args = append(args, s.Unit)
	}
	show, err := run(ctx, "systemctl", args...)
	if err != nil {
		return svcs, nil // the list alone is still useful
	}
	props := parseShow(show)
	for i := range svcs {
		p := props[svcs[i].Unit]
		svcs[i].Enabled = p["UnitFileState"]
		if m, err := strconv.ParseUint(p["MemoryCurrent"], 10, 64); err == nil && m != 1<<64-1 {
			svcs[i].Memory = m
		}
	}
	return svcs, nil
}

func parseListUnits(out string) []protocol.SystemdService {
	svcs := []protocol.SystemdService{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "●" {
			f = f[1:]
		}
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") || f[1] == "not-found" {
			continue
		}
		svcs = append(svcs, protocol.SystemdService{Unit: f[0], Load: f[1], Active: f[2], Sub: f[3], Description: strings.Join(f[4:], " ")})
	}
	return svcs
}

// parseShow reads `systemctl show` output for several units: key=value blocks separated by blank lines.
func parseShow(out string) map[string]map[string]string {
	res := map[string]map[string]string{}
	for _, block := range strings.Split(out, "\n\n") {
		kv := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				kv[k] = v
			}
		}
		if kv["Id"] != "" {
			res[kv["Id"]] = kv
		}
	}
	return res
}

// ownUnit is the unit the agent runs under, in case it was installed under another name.
func ownUnit() string {
	b, _ := os.ReadFile("/proc/self/cgroup")
	for _, line := range strings.Split(string(b), "\n") {
		if u := path.Base(line); strings.HasSuffix(u, ".service") {
			return u
		}
	}
	return agentUnit
}

func refuseServiceAction(args protocol.ServiceArgs, own string) error {
	if (args.Unit == own || args.Unit == agentUnit) && args.Action != "start" {
		return fmt.Errorf("the agent can't %s its own service: it would disconnect before reporting back", args.Action)
	}
	if args.Action == "stop" && lockoutUnits[args.Unit] {
		return fmt.Errorf("stopping %s could lock you out of this server, so Orbit doesn't allow it", args.Unit)
	}
	return nil
}

func (a *Agent) serviceAction(ctx context.Context, args protocol.ServiceArgs) error {
	if a.cfg.HostRoot != "" {
		return errContainerMode
	}
	if err := args.Validate(); err != nil {
		return err
	}
	if err := refuseServiceAction(args, ownUnit()); err != nil {
		return err
	}
	svcs, err := listServices(ctx)
	if err != nil {
		return err
	}
	known := false
	for _, s := range svcs {
		known = known || s.Unit == args.Unit
	}
	if !known {
		return fmt.Errorf("%s is not a service on this server", args.Unit)
	}
	_, err = run(ctx, "systemctl", args.Action, "--", args.Unit)
	return err
}
