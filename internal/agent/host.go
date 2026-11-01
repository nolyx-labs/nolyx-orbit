package agent

import (
	"net"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"orbit/internal/protocol"
)

// Host reads metrics from /proc. Root lets the agent run in a container with the host mounted at e.g. /host.
// shortcut: Linux only (the agent's target); macOS/Windows hosts report zeros.
type Host struct {
	Root     string
	prevCPU  [2]uint64            // idle, total
	prevNet  [2]uint64            // rx, tx
	prevIO   map[string][3]uint64 // disk → sectors read, sectors written, ms doing I/O
	prevTime time.Time
}

var wholeDisk = regexp.MustCompile(`^(sd[a-z]+|vd[a-z]+|xvd[a-z]+|nvme[0-9]+n[0-9]+|mmcblk[0-9]+)$`)

func (h *Host) read(path string) string {
	b, _ := os.ReadFile(h.Root + path)
	return string(b)
}

func (h *Host) Hello(agentVersion, dockerVersion string) protocol.Hello {
	hostname, _ := os.Hostname()
	if s := strings.TrimSpace(h.read("/etc/hostname")); s != "" {
		hostname = s
	}
	osName := runtime.GOOS
	for _, line := range strings.Split(h.read("/etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			osName = strings.Trim(v, `"`)
		}
	}
	m := h.Sample()
	return protocol.Hello{
		Hostname: hostname, IP: primaryIP(), OS: osName, Arch: runtime.GOARCH,
		Kernel:   strings.TrimSpace(h.read("/proc/sys/kernel/osrelease")),
		CPUCount: runtime.NumCPU(), MemTotal: m.MemTotal, DiskTotal: m.DiskTotal,
		AgentVersion: agentVersion, DockerVersion: dockerVersion,
	}
}

// Sample returns current metrics; rates are computed against the previous call.
func (h *Host) Sample() protocol.Metrics {
	now := time.Now().UTC()
	m := protocol.Metrics{TS: now}

	if f := strings.Fields(firstLine(h.read("/proc/stat"))); len(f) > 4 && f[0] == "cpu" {
		var total uint64
		for _, v := range f[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			total += n
		}
		idle, _ := strconv.ParseUint(f[4], 10, 64)
		if len(f) > 5 { // iowait counts as idle
			iow, _ := strconv.ParseUint(f[5], 10, 64)
			idle += iow
		}
		if dt := total - h.prevCPU[1]; h.prevCPU[1] > 0 && dt > 0 {
			m.CPU = 100 * (1 - float64(idle-h.prevCPU[0])/float64(dt))
		}
		h.prevCPU = [2]uint64{idle, total}
	}

	mem := map[string]uint64{}
	for _, line := range strings.Split(h.read("/proc/meminfo"), "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			mem[strings.TrimSuffix(f[0], ":")] = n * 1024
		}
	}
	m.MemTotal = mem["MemTotal"]
	if m.MemTotal > mem["MemAvailable"] {
		m.MemUsed = m.MemTotal - mem["MemAvailable"]
	}

	var st syscall.Statfs_t
	if syscall.Statfs(h.Root+"/", &st) == nil {
		bs := uint64(st.Bsize)
		m.DiskTotal = st.Blocks * bs
		m.DiskUsed = (st.Blocks - st.Bfree) * bs
	}

	var rx, tx uint64
	for _, line := range strings.Split(h.read("/proc/net/dev"), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
			continue
		}
		if f := strings.Fields(rest); len(f) >= 9 {
			r, _ := strconv.ParseUint(f[0], 10, 64)
			t, _ := strconv.ParseUint(f[8], 10, 64)
			rx, tx = rx+r, tx+t
		}
	}
	if secs := now.Sub(h.prevTime).Seconds(); !h.prevTime.IsZero() && secs > 0 && rx >= h.prevNet[0] && tx >= h.prevNet[1] {
		m.NetRx = uint64(float64(rx-h.prevNet[0]) / secs)
		m.NetTx = uint64(float64(tx-h.prevNet[1]) / secs)
	}
	io := map[string][3]uint64{}
	for _, line := range strings.Split(h.read("/proc/diskstats"), "\n") {
		f := strings.Fields(line)
		if len(f) < 13 || !wholeDisk.MatchString(f[2]) {
			continue
		}
		var v [3]uint64
		for i, idx := range []int{5, 9, 12} {
			v[i], _ = strconv.ParseUint(f[idx], 10, 64)
		}
		io[f[2]] = v
	}
	if secs := now.Sub(h.prevTime).Seconds(); !h.prevTime.IsZero() && secs > 0 {
		for disk, cur := range io {
			prev, ok := h.prevIO[disk]
			if !ok || cur[0] < prev[0] || cur[1] < prev[1] || cur[2] < prev[2] {
				continue
			}
			m.IORead += uint64(float64(cur[0]-prev[0]) * 512 / secs)
			m.IOWrite += uint64(float64(cur[1]-prev[1]) * 512 / secs)
			m.IOUtil = max(m.IOUtil, min(100, float64(cur[2]-prev[2])/(secs*10)))
		}
	}
	h.prevIO = io
	h.prevNet, h.prevTime = [2]uint64{rx, tx}, now

	if f := strings.Fields(h.read("/proc/loadavg")); len(f) > 0 {
		m.Load1, _ = strconv.ParseFloat(f[0], 64)
	}
	if f := strings.Fields(h.read("/proc/uptime")); len(f) > 0 {
		up, _ := strconv.ParseFloat(f[0], 64)
		m.Uptime = uint64(up)
	}
	return m
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func primaryIP() string {
	// No packets are sent; this just asks the kernel which source address it would route from.
	if c, err := net.Dial("udp", "1.1.1.1:53"); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	return ""
}
