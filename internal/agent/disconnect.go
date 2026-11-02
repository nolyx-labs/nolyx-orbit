// orbit-agent disconnect: remove the agent from a server installed with install.sh, leaving every container
// and service it deployed running.
package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Disconnect runs the disconnect command as root and returns the exit code.
func Disconnect(stateFile string, out io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(out, "run as root (sudo orbit-agent disconnect)")
		return 1
	}
	if os.Getenv("ORBIT_HOST_ROOT") != "" {
		fmt.Fprintln(out, "this agent runs in a container: remove the container and its nolyx-orbit-agent volume instead")
		return 1
	}
	run := func(args ...string) error { return exec.Command(args[0], args[1:]...).Run() }
	return disconnect("/", stateFile, run, out)
}

// disconnect does the work under root (a temporary directory in tests).
func disconnect(root, stateFile string, run func(args ...string) error, out io.Writer) int {
	at := func(p string) string { return filepath.Join(root, p) }
	failed := false
	step := func(what string, err error) {
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(out, "✗ %s: %v\n", what, err)
			failed = true
		} else {
			fmt.Fprintf(out, "✓ %s\n", what)
		}
	}
	// Stopping the service ends the connection; deployed containers keep their own restart policies.
	run("systemctl", "disable", "--now", "orbit-agent")
	step("stopped the orbit-agent service", nil)
	step("removed the service unit", os.Remove(at("/etc/systemd/system/orbit-agent.service")))
	run("systemctl", "daemon-reload")
	step("removed /etc/orbit-agent (URL and token)", os.RemoveAll(at("/etc/orbit-agent")))
	step("removed the server identity", os.Remove(at(stateFile)))
	if _, err := os.Stat(at(receiveRoot)); err == nil {
		// The orbit-backups SSH user's forced command is this binary; removing it would break incoming backups.
		fmt.Fprintf(out, "• kept /usr/local/bin/orbit-agent: this server stores backups for others in %s\n", receiveRoot)
	} else {
		step("removed /usr/local/bin/orbit-agent", os.Remove(at("/usr/local/bin/orbit-agent")))
	}
	fmt.Fprintf(out, "• kept the action log in %s\n", filepath.Join(filepath.Dir(stateFile), "actions.log"))
	fmt.Fprintln(out, "Containers and services are untouched. Remove the server in Orbit to delete its history there.")
	if failed {
		return 1
	}
	return 0
}
