// Listening ports from /proc/net, with the owning process and container.
package agent

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"orbit/internal/protocol"
)

var containerIDRe = regexp.MustCompile(`[0-9a-f]{64}`)

type procSock struct {
	protocol.ListenPort
	inode string
}

// parseProcNet keeps listening TCP sockets (state 0A) and unbound-peer UDP sockets (07).
// shortcut: assumes a little-endian host (amd64/arm64, the agent's targets).
func parseProcNet(data, proto string) []procSock {
	want := "0A"
	if proto == "udp" {
		want = "07"
	}
	var out []procSock
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[3] != want {
			continue
		}
		ip, port, ok := decodeAddr(f[1])
		if !ok {
			continue
		}
		scope := "specific"
		switch {
		case ip.IsUnspecified():
			scope = "all"
		case ip.IsLoopback():
			scope = "localhost"
		}
		out = append(out, procSock{protocol.ListenPort{Proto: proto, Address: ip.String(), Port: port, Scope: scope}, f[9]})
	}
	return out
}

// decodeAddr decodes "0100007F:0035": the address is stored as host-order 32-bit words.
func decodeAddr(s string) (net.IP, int, bool) {
	ipHex, portHex, ok := strings.Cut(s, ":")
	b, err := hex.DecodeString(ipHex)
	if !ok || err != nil || (len(b) != 4 && len(b) != 16) {
		return nil, 0, false
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	return net.IP(b), int(port), err == nil
}

// socketOwners maps socket inode → pid by reading every process's fd links.
func (h *Host) socketOwners() map[string]int {
	owners := map[string]int{}
	procs, _ := filepath.Glob(h.Root + "/proc/[0-9]*")
	for _, p := range procs {
		pid, _ := strconv.Atoi(filepath.Base(p))
		fds, _ := os.ReadDir(p + "/fd")
		for _, fd := range fds {
			link, _ := os.Readlink(p + "/fd/" + fd.Name())
			if inode, ok := strings.CutPrefix(link, "socket:["); ok {
				owners[strings.TrimSuffix(inode, "]")] = pid
			}
		}
	}
	return owners
}

// Ports reads the host network namespace through pid 1, so it works from the agent container too.
func (h *Host) Ports(cs []dockerContainer) []protocol.ListenPort {
	byID, published := map[string]string{}, map[string]string{}
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		byID[c.ID] = name
		for _, p := range c.Ports {
			if p.PublicPort != 0 {
				published[fmt.Sprintf("%s/%d", p.Type, p.PublicPort)] = name
			}
		}
	}
	owners := h.socketOwners()
	seen := map[string]bool{}
	out := []protocol.ListenPort{}
	for _, file := range []string{"tcp", "tcp6", "udp", "udp6"} {
		for _, s := range parseProcNet(h.read("/proc/1/net/"+file), strings.TrimSuffix(file, "6")) {
			key := fmt.Sprintf("%s/%s/%d", s.Proto, s.Address, s.Port)
			if seen[key] {
				continue // SO_REUSEPORT workers share a port
			}
			seen[key] = true
			if pid := owners[s.inode]; pid > 0 {
				s.PID = pid
				s.Process = strings.TrimSpace(h.read(fmt.Sprintf("/proc/%d/comm", pid)))
				s.Container = byID[containerIDRe.FindString(h.read(fmt.Sprintf("/proc/%d/cgroup", pid)))]
			}
			if s.Container == "" {
				s.Container = published[fmt.Sprintf("%s/%d", s.Proto, s.Port)]
			}
			out = append(out, s.ListenPort)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}
