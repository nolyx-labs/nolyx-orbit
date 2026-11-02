// OS package updates with apt (Debian/Ubuntu) or dnf (Fedora/RHEL family).
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"orbit/internal/protocol"
)

var errNeedsInstall = errors.New("OS updates need the agent installed with install.sh (it can't manage packages from a container)")

// packageManager returns apt or dnf. The container agent only sees the host read-only, so it can't.
func (a *Agent) packageManager() (string, error) {
	if a.cfg.HostRoot != "" {
		return "", errNeedsInstall
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return "", errNeedsInstall
	}
	for _, m := range []string{"apt-get", "dnf"} {
		if _, err := exec.LookPath(m); err == nil {
			return strings.TrimSuffix(m, "-get"), nil
		}
	}
	return "", errors.New("no supported package manager found (OS updates need apt or dnf)")
}

func (a *Agent) osExec(ctx context.Context, action string, raw json.RawMessage, progress func(protocol.Progress)) (any, error) {
	mgr, err := a.packageManager()
	if err != nil {
		return nil, err
	}
	switch action {
	case protocol.ActionOSCheck:
		refresh(ctx, mgr)
		return osCheck(ctx, mgr)
	case protocol.ActionOSInstall:
		var args protocol.OSInstallArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		res, err := osInstall(ctx, mgr, args, progress)
		if res.Rebooting {
			a.reboot()
		}
		return res, err
	case protocol.ActionOSHold:
		var args protocol.OSHoldArgs
		if err := json.Unmarshal(raw, &args); err != nil || !protocol.ValidPackage(args.Package) {
			return nil, errors.New("invalid package name")
		}
		cmd := []string{"apt-mark", "unhold", args.Package}
		if args.Hold {
			cmd[1] = "hold"
		}
		if mgr == "dnf" {
			cmd = []string{"versionlock", "delete", args.Package}
			if args.Hold {
				cmd[1] = "add"
			}
			cmd = append([]string{"dnf", "-q"}, cmd...)
		}
		if _, err := packageRun(ctx, cmd[0], cmd[1:]...); err != nil {
			return nil, err
		}
		return heldPackages(ctx, mgr), nil
	case protocol.ActionOSReboot:
		a.reboot()
		return nil, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

// refresh updates package lists, bounded so a check fits the control plane's wait. On timeout the
// existing lists are used.
func refresh(ctx context.Context, mgr string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if mgr == "apt" {
		_, err := packageRun(ctx, "apt-get", "update", "-q")
		return err
	}
	_, err := packageRun(ctx, "dnf", "-q", "makecache")
	return err
}

func osCheck(ctx context.Context, mgr string) (protocol.OSCheckResult, error) {
	res := protocol.OSCheckResult{Manager: mgr, Held: heldPackages(ctx, mgr)}
	if mgr == "apt" {
		out, err := packageRun(ctx, "apt", "list", "--upgradable")
		if err != nil {
			return res, err
		}
		res.Packages = parseAptUpgradable(out)
		_, err = os.Stat("/var/run/reboot-required")
		res.RebootRequired = err == nil
		return res, nil
	}
	out, err := packageRun(ctx, "dnf", "-C", "-q", "check-update")
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 100) { // 100: updates available
		return res, err
	}
	res.Packages = parseDnfCheckUpdate(out)
	if len(res.Packages) > 0 {
		names := make([]string, len(res.Packages))
		for i, p := range res.Packages {
			names[i] = p.Name
		}
		rpm, _ := packageRun(ctx, "rpm", append([]string{"-q", "--qf", `%{NAME} %|EPOCH?{%{EPOCH}:}:{}|%{VERSION}-%{RELEASE}\n`}, names...)...)
		installed := parseRpmVersions(rpm)
		for i := range res.Packages {
			res.Packages[i].Installed = installed[res.Packages[i].Name]
		}
		sec, _ := packageRun(ctx, "dnf", "-C", "-q", "updateinfo", "list", "--security")
		markDnfSecurity(res.Packages, sec)
	}
	nr, _ := packageRun(ctx, "dnf", "-q", "needs-restarting", "-r") // exit 1 means "reboot required", so read the text
	res.RebootRequired = dnfRebootRequired(nr)
	return res, nil
}

func heldPackages(ctx context.Context, mgr string) []string {
	if mgr == "apt" {
		out, _ := packageRun(ctx, "apt-mark", "showhold")
		return strings.Fields(out)
	}
	out, _ := packageRun(ctx, "dnf", "-q", "versionlock", "list") // fails without the versionlock plugin: nothing is held
	return parseVersionlock(out)
}

// installCommands turns the request into apt/dnf invocations. pending is the current check, for all/security on apt.
func installCommands(mgr string, args protocol.OSInstallArgs, pending protocol.OSCheckResult) ([][]string, error) {
	held := map[string]bool{}
	for _, h := range pending.Held {
		held[h] = true
	}
	var latest, exact []string
	switch args.Scope {
	case "all", "security":
		if mgr == "dnf" {
			if args.Scope == "security" {
				return [][]string{{"dnf", "upgrade", "-y", "--security"}}, nil
			}
			return [][]string{{"dnf", "upgrade", "-y"}}, nil
		}
		// apt-get upgrade keeps back updates that need new packages (such as a new kernel), so name them instead.
		for _, p := range pending.Packages {
			if !held[p.Name] && (args.Scope == "all" || p.Security) {
				latest = append(latest, p.Name)
			}
		}
	case "packages":
		if len(args.Packages) == 0 {
			return nil, errors.New("no packages chosen")
		}
		for _, p := range args.Packages {
			if !protocol.ValidPackage(p.Name) || (p.Version != "" && !protocol.ValidVersion(p.Version)) {
				return nil, fmt.Errorf("invalid package %q", p.Name)
			}
			if held[p.Name] {
				return nil, fmt.Errorf("%s is held at its current version: let it go first", p.Name)
			}
			switch {
			case p.Version == "":
				latest = append(latest, p.Name)
			case mgr == "apt":
				exact = append(exact, p.Name+"="+p.Version)
			default:
				exact = append(exact, p.Name+"-"+p.Version)
			}
		}
	default:
		return nil, errors.New("scope must be all, security or packages")
	}
	var cmds [][]string
	if mgr == "apt" {
		if len(latest)+len(exact) > 0 {
			cmd := []string{"apt-get", "install", "-y", "--only-upgrade", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
			if len(exact) > 0 {
				cmd = append(cmd, "--allow-downgrades")
			}
			cmds = append(cmds, append(cmd, append(latest, exact...)...))
		}
		return cmds, nil
	}
	if len(latest) > 0 {
		cmds = append(cmds, append([]string{"dnf", "upgrade", "-y"}, latest...))
	}
	if len(exact) > 0 { // dnf install of a specific version upgrades or downgrades to it
		cmds = append(cmds, append([]string{"dnf", "install", "-y"}, exact...))
	}
	return cmds, nil
}

func osInstall(ctx context.Context, mgr string, args protocol.OSInstallArgs, progress func(protocol.Progress)) (protocol.OSInstallResult, error) {
	var res protocol.OSInstallResult
	if args.Reboot != "never" && args.Reboot != "if_required" {
		return res, errors.New("reboot must be never or if_required")
	}
	step := stepper(progress)
	plan := protocol.OSInstallPlan
	if err := step(plan[0], func() error { return refresh(ctx, mgr) }); err != nil {
		return res, err
	}
	if err := step(plan[1], func() error {
		pending, err := osCheck(ctx, mgr)
		if err != nil {
			return err
		}
		cmds, err := installCommands(mgr, args, pending)
		if err != nil {
			return err
		}
		if len(cmds) == 0 {
			progress(protocol.Progress{Step: plan[1], Status: "running", Line: "Nothing to install"})
		}
		for _, c := range cmds {
			progress(protocol.Progress{Step: plan[1], Status: "running", Line: "$ " + strings.Join(c, " ")})
			if err := stream(ctx, plan[1], progress, c[0], c[1:]...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return res, err
	}
	err := step(plan[2], func() (err error) { res.Check, err = osCheck(ctx, mgr); return })
	res.Rebooting = err == nil && args.Reboot == "if_required" && res.Check.RebootRequired
	return res, err
}

// reboot restarts the server a few seconds from now, after the result has been sent.
func (a *Agent) reboot() {
	go func() {
		time.Sleep(5 * time.Second)
		if out, err := exec.Command("systemctl", "reboot").CombinedOutput(); err != nil {
			a.log.Error("reboot failed", "err", err, "out", string(out))
		}
	}()
}

func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	return c
}

// run returns stdout. Errors carry the end of stderr; the *exec.ExitError stays reachable for exit codes.
func packageRun(ctx context.Context, name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	c := command(ctx, name, args...)
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s: %w: %s", name, err, tail(stderr.String(), 5))
	}
	return stdout.String(), nil
}

// stream runs a command and reports each output line as progress.
func stream(ctx context.Context, step string, progress func(protocol.Progress), name string, args ...string) error {
	c := command(ctx, name, args...)
	out, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	c.Stderr = c.Stdout
	if err := c.Start(); err != nil {
		return err
	}
	var last []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		progress(protocol.Progress{Step: step, Status: "running", Line: sc.Text()})
		last = append(last, sc.Text())
		if len(last) > 10 {
			last = last[1:]
		}
	}
	if err := c.Wait(); err != nil {
		return fmt.Errorf("%s failed (%v):\n%s", name, err, strings.Join(last, "\n"))
	}
	return nil
}

// parseAptUpgradable reads `apt list --upgradable`:
// "openssl/jammy-updates,jammy-security 3.0.2-0ubuntu1.15 amd64 [upgradable from: 3.0.2-0ubuntu1.14]".
func parseAptUpgradable(out string) []protocol.OSPackage {
	var pkgs []protocol.OSPackage
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		from := strings.Index(line, "[upgradable from: ")
		if len(f) < 3 || from < 0 {
			continue
		}
		name, suites, _ := strings.Cut(f[0], "/")
		if seen[name] { // one entry per architecture
			continue
		}
		seen[name] = true
		pkgs = append(pkgs, protocol.OSPackage{
			Name: name, Available: f[1], Security: strings.Contains(suites, "-security"),
			Installed: strings.TrimSuffix(strings.TrimSpace(line[from+len("[upgradable from: "):]), "]"),
		})
	}
	return pkgs
}

// parseDnfCheckUpdate reads `dnf check-update`: "name.arch  version  repo" rows, which dnf wraps onto
// two lines when the name is long. It stops at the obsoletes section.
func parseDnfCheckUpdate(out string) []protocol.OSPackage {
	var pkgs []protocol.OSPackage
	seen := map[string]bool{}
	var pending []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Obsoleting Packages") {
			break
		}
		if strings.HasPrefix(line, "Last metadata expiration") || strings.HasPrefix(line, "Security:") {
			continue
		}
		pending = append(pending, strings.Fields(line)...)
		if len(pending) < 3 {
			continue
		}
		nameArch, version := pending[0], pending[1]
		pending = pending[3:]
		dot := strings.LastIndex(nameArch, ".")
		if dot <= 0 {
			continue
		}
		name := nameArch[:dot]
		if !seen[name] {
			seen[name] = true
			pkgs = append(pkgs, protocol.OSPackage{Name: name, Available: version})
		}
	}
	return pkgs
}

func parseRpmVersions(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if name, v, ok := strings.Cut(strings.TrimSpace(line), " "); ok && m[name] == "" {
			m[name] = v
		}
	}
	return m
}

// markDnfSecurity flags packages named in `dnf updateinfo list --security`. dnf4 and dnf5 order the
// columns differently, so any field that is the NEVRA of a pending package counts.
func markDnfSecurity(pkgs []protocol.OSPackage, out string) {
	idx := map[string]int{}
	for i, p := range pkgs {
		idx[p.Name] = i
	}
	for _, line := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(line) {
			if i, ok := idx[rpmName(f)]; ok {
				pkgs[i].Security = true
			}
		}
	}
}

var rpmArches = []string{".x86_64", ".aarch64", ".noarch", ".i686", ".ppc64le", ".s390x", ".src", ".*"}

// rpmName strips "-[epoch:]version-release[.arch]" from a NEVRA, leaving the package name.
func rpmName(nevra string) string {
	for _, a := range rpmArches {
		nevra = strings.TrimSuffix(nevra, a)
	}
	i := strings.LastIndex(nevra, "-")
	if i <= 0 {
		return ""
	}
	j := strings.LastIndex(nevra[:i], "-")
	if j <= 0 {
		return ""
	}
	return nevra[:j]
}

// parseVersionlock reads `dnf versionlock list`: dnf4 prints "bash-0:5.1.8-6.el9.*", dnf5 "Package name: bash".
func parseVersionlock(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if n, ok := strings.CutPrefix(line, "Package name: "); ok {
			names = append(names, n)
		} else if strings.HasSuffix(line, ".*") && !strings.HasPrefix(line, "#") {
			if n := rpmName(line); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

func dnfRebootRequired(out string) bool { return strings.Contains(out, "Reboot is required") }
