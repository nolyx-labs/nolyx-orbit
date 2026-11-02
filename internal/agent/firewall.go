// Firewall: read ufw or firewalld, open and close ports.
package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"orbit/internal/protocol"
)

var (
	columns     = regexp.MustCompile(`\s{2,}`)
	richSrc     = regexp.MustCompile(`source address="([^"]+)"`)
	richPort    = regexp.MustCompile(`port port="([^"]+)" protocol="([^"]+)"`)
	richVerdict = regexp.MustCompile(`\b(accept|reject|drop)\s*$`)
)

func readFirewall(ctx context.Context) (protocol.Firewall, error) {
	switch {
	case has("ufw"):
		out, err := run(ctx, "ufw", "status", "verbose")
		if err != nil {
			return protocol.Firewall{Tool: "ufw"}, err
		}
		return parseUFW(out), nil
	case has("firewall-cmd"):
		if state, _ := run(ctx, "firewall-cmd", "--state"); strings.TrimSpace(state) != "running" {
			return protocol.Firewall{Tool: "firewalld", Rules: []protocol.FirewallRule{}}, nil
		}
		out, err := run(ctx, "firewall-cmd", "--list-all")
		if err != nil {
			return protocol.Firewall{Tool: "firewalld"}, err
		}
		return parseFirewalld(out), nil
	}
	return protocol.Firewall{Rules: []protocol.FirewallRule{}}, nil
}

func parseUFW(out string) protocol.Firewall {
	fw := protocol.Firewall{Tool: "ufw", Rules: []protocol.FirewallRule{}}
	inRules := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "Status:"):
			fw.Active = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
		case strings.HasPrefix(line, "Default:"):
			for _, part := range strings.Split(strings.TrimPrefix(line, "Default:"), ",") {
				if f := strings.Fields(part); len(f) == 2 {
					switch f[1] {
					case "(incoming)":
						fw.Incoming = f[0]
					case "(outgoing)":
						fw.Outgoing = f[0]
					}
				}
			}
		case strings.HasPrefix(line, "--"):
			inRules = true
		case inRules && strings.TrimSpace(line) != "":
			cols := columns.Split(strings.TrimSpace(line), -1)
			if len(cols) < 3 {
				continue
			}
			r := protocol.FirewallRule{Action: cols[1], From: strings.TrimSuffix(cols[2], " (v6)"), Raw: strings.TrimSpace(line)}
			to := cols[0]
			if r.V6 = strings.HasSuffix(to, " (v6)"); r.V6 {
				to = strings.TrimSuffix(to, " (v6)")
			}
			// "10.0.0.5 22/tcp" names a destination address; app profiles like "Nginx Full" contain spaces too.
			if f := strings.Fields(to); len(f) > 1 && strings.ContainsAny(f[len(f)-1], "0123456789") {
				to = f[len(f)-1]
			}
			r.Port, r.Proto, _ = strings.Cut(to, "/")
			fw.Rules = append(fw.Rules, r)
		}
	}
	return fw
}

// parseFirewalld reads `firewall-cmd --list-all` for the default zone.
func parseFirewalld(out string) protocol.Firewall {
	fw := protocol.Firewall{Tool: "firewalld", Active: true, Incoming: "reject", Outgoing: "allow", Rules: []protocol.FirewallRule{}}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "rule ") {
			r := protocol.FirewallRule{From: "Anywhere", Raw: line, Action: "ALLOW IN"}
			if m := richSrc.FindStringSubmatch(line); m != nil {
				r.From = m[1]
			}
			if m := richPort.FindStringSubmatch(line); m != nil {
				r.Port, r.Proto = strings.ReplaceAll(m[1], "-", ":"), m[2]
			}
			if m := richVerdict.FindStringSubmatch(line); m != nil && m[1] != "accept" {
				r.Action = strings.ToUpper(m[1]) + " IN"
			}
			fw.Rules = append(fw.Rules, r)
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		v = strings.TrimSpace(v)
		switch k {
		case "target":
			switch v {
			case "ACCEPT":
				fw.Incoming = "allow"
			case "DROP":
				fw.Incoming = "deny"
			}
		case "services":
			for _, s := range strings.Fields(v) {
				fw.Rules = append(fw.Rules, protocol.FirewallRule{Port: s, Action: "ALLOW IN", From: "Anywhere", Raw: "service " + s})
			}
		case "ports":
			for _, p := range strings.Fields(v) {
				port, proto, _ := strings.Cut(p, "/")
				fw.Rules = append(fw.Rules, protocol.FirewallRule{Port: strings.ReplaceAll(port, "-", ":"), Proto: proto, Action: "ALLOW IN", From: "Anywhere", Raw: "port " + p})
			}
		}
	}
	return fw
}

// refuseFirewall blocks closing the SSH port. Orbit only adds and removes inbound allow rules and never touches
// the outgoing policy, so the agent's outbound connection can't be cut by these commands.
func refuseFirewall(args protocol.FirewallArgs, sshPorts []int) error {
	if len(sshPorts) == 0 {
		sshPorts = []int{22}
	}
	if args.Action == "close" && args.Proto == "tcp" && slices.Contains(sshPorts, args.Port) {
		return fmt.Errorf("port %d is the SSH port in use; closing it could lock you out of this server", args.Port)
	}
	return nil
}

// firewallCommands builds the argument lists for an already validated change.
func firewallCommands(tool string, args protocol.FirewallArgs) [][]string {
	port := strconv.Itoa(args.Port)
	if tool == "ufw" {
		verb := []string{"ufw", "allow"}
		if args.Action == "close" {
			verb = []string{"ufw", "--force", "delete", "allow"}
		}
		src := "any"
		if args.Source != "" {
			src = args.Source
		}
		return [][]string{append(verb, "proto", args.Proto, "from", src, "to", "any", "port", port)}
	}
	op := "--add-"
	if args.Action == "close" {
		op = "--remove-"
	}
	rule := op + "port=" + port + "/" + args.Proto
	if args.Source != "" {
		family := "ipv4"
		if strings.Contains(args.Source, ":") {
			family = "ipv6"
		}
		rule = fmt.Sprintf(`%srich-rule=rule family="%s" source address="%s" port port="%s" protocol="%s" accept`, op, family, args.Source, port, args.Proto)
	}
	// Runtime and permanent separately, so a --reload doesn't discard other runtime-only rules.
	return [][]string{{"firewall-cmd", rule}, {"firewall-cmd", "--permanent", rule}}
}

func (a *Agent) firewallChange(ctx context.Context, args protocol.FirewallArgs) error {
	if a.cfg.HostRoot != "" {
		return errContainerMode
	}
	if err := args.Validate(); err != nil {
		return err
	}
	fw, err := readFirewall(ctx)
	if err != nil {
		return err
	}
	if fw.Tool == "" {
		return errors.New("no firewall is installed: install ufw (apt install ufw) or firewalld (dnf install firewalld) first")
	}
	if fw.Tool == "firewalld" && !fw.Active {
		return errors.New("firewalld is not running: start it with systemctl enable --now firewalld")
	}
	if err := refuseFirewall(args, sshConfig(ctx).Ports); err != nil {
		return err
	}
	for _, c := range firewallCommands(fw.Tool, args) {
		if _, err := run(ctx, c[0], c[1:]...); err != nil {
			return err
		}
	}
	return nil
}
