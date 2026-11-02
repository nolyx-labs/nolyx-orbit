package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orbit/internal/protocol"
)

func TestParseListUnitsAndShow(t *testing.T) {
	list := `accounts-daemon.service                  loaded    active   running Accounts Service
apparmor.service                         loaded    active   exited  Load AppArmor profiles
● certbot.service                        loaded    failed   failed  Certbot
docker.service                           loaded    active   running Docker Application Container Engine
plymouth-quit-wait.service               not-found inactive dead    plymouth-quit-wait.service
ssh.service                              loaded    active   running OpenBSD Secure Shell server
`
	svcs := parseListUnits(list)
	if len(svcs) != 5 {
		t.Fatalf("got %d services: %+v", len(svcs), svcs)
	}
	if c := svcs[2]; c.Unit != "certbot.service" || c.Active != "failed" || c.Sub != "failed" || c.Description != "Certbot" {
		t.Errorf("failed unit parsed as %+v", c)
	}
	if svcs[3].Description != "Docker Application Container Engine" {
		t.Errorf("description %q", svcs[3].Description)
	}
	show := "Id=docker.service\nUnitFileState=enabled\nMemoryCurrent=123456789\n\nId=apparmor.service\nUnitFileState=enabled\nMemoryCurrent=[not set]\n"
	p := parseShow(show)
	if p["docker.service"]["MemoryCurrent"] != "123456789" || p["apparmor.service"]["UnitFileState"] != "enabled" {
		t.Errorf("parseShow = %v", p)
	}
}

func TestServiceRefusals(t *testing.T) {
	for _, c := range []struct {
		args protocol.ServiceArgs
		ok   bool
	}{
		{protocol.ServiceArgs{Unit: "nginx.service", Action: "stop"}, true},
		{protocol.ServiceArgs{Unit: "sshd.service", Action: "restart"}, true},
		{protocol.ServiceArgs{Unit: "ssh.service", Action: "stop"}, false},
		{protocol.ServiceArgs{Unit: "sshd.service", Action: "stop"}, false},
		{protocol.ServiceArgs{Unit: "orbit-agent.service", Action: "restart"}, false},
		{protocol.ServiceArgs{Unit: "my-agent.service", Action: "stop"}, false}, // its own unit under another name
		{protocol.ServiceArgs{Unit: "NetworkManager.service", Action: "stop"}, false},
	} {
		if err := refuseServiceAction(c.args, "my-agent.service"); (err == nil) != c.ok {
			t.Errorf("%+v: err = %v", c.args, err)
		}
	}
}

func TestParseProcNet(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 21345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1538 00000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 33410 1 0000000000000000 100 0 0 10 0
   2: 0A00000F:0016 0A000001:D2F0 01 00000000:00000000 02:000A3C1E 00000000     0        0 44001 4 0000000000000000 20 4 29 10 -1
   3: 0F00000A:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 50001 1 0000000000000000 100 0 0 10 0
`
	got := parseProcNet(tcp, "tcp")
	want := []protocol.ListenPort{
		{Proto: "tcp", Address: "0.0.0.0", Port: 22, Scope: "all"},
		{Proto: "tcp", Address: "127.0.0.1", Port: 5432, Scope: "localhost"},
		{Proto: "tcp", Address: "10.0.0.15", Port: 8080, Scope: "specific"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].ListenPort != want[i] {
			t.Errorf("%d: got %+v want %+v", i, got[i].ListenPort, want[i])
		}
	}
	if got[0].inode != "21345" {
		t.Errorf("inode %q", got[0].inode)
	}
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22001 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:0277 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22002 1 0000000000000000 100 0 0 10 0
`
	got = parseProcNet(tcp6, "tcp")
	if len(got) != 2 || got[0].Address != "::" || got[0].Scope != "all" || got[0].Port != 80 || got[1].Address != "::1" || got[1].Scope != "localhost" || got[1].Port != 631 {
		t.Errorf("tcp6: %+v", got)
	}
	udp := `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  411: 3500007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 19455 2 0000000000000000 0
  726: 00000000:0044 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 19899 2 0000000000000000 0
`
	got = parseProcNet(udp, "udp")
	if len(got) != 2 || got[0].Address != "127.0.0.53" || got[0].Port != 53 || got[1].Port != 68 || got[1].Scope != "all" {
		t.Errorf("udp: %+v", got)
	}
}

func TestParseMounts(t *testing.T) {
	mounts := `/dev/sda1 / ext4 rw,relatime,discard,errors=remount-ro 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
/dev/sda15 /boot/efi vfat rw,relatime 0 0
/dev/loop0 /snap/core20/2105 squashfs ro,nodev,relatime 0 0
/dev/sdb /mnt/data\040disk xfs rw,relatime 0 0
/dev/sda1 /var/lib/docker/plugins ext4 rw,relatime 0 0
overlay /var/lib/docker/overlay2/x/merged overlay rw 0 0
`
	got := parseMounts(mounts)
	want := []protocol.Disk{
		{Device: "/dev/sda1", Mount: "/", FS: "ext4"},
		{Device: "/dev/sda15", Mount: "/boot/efi", FS: "vfat"},
		{Device: "/dev/sdb", Mount: "/mnt/data disk", FS: "xfs"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestParseUFW(t *testing.T) {
	out := `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
80,443/tcp                 ALLOW IN    Anywhere
5432/tcp                   ALLOW IN    10.0.0.0/8
Nginx Full                 ALLOW IN    Anywhere
10.0.0.5 9000/tcp          ALLOW IN    Anywhere
22/tcp (v6)                ALLOW IN    Anywhere (v6)
`
	fw := parseUFW(out)
	if !fw.Active || fw.Incoming != "deny" || fw.Outgoing != "allow" || len(fw.Rules) != 6 {
		t.Fatalf("got %+v", fw)
	}
	for i, want := range []protocol.FirewallRule{
		{Port: "22", Proto: "tcp", Action: "ALLOW IN", From: "Anywhere"},
		{Port: "80,443", Proto: "tcp", Action: "ALLOW IN", From: "Anywhere"},
		{Port: "5432", Proto: "tcp", Action: "ALLOW IN", From: "10.0.0.0/8"},
		{Port: "Nginx Full", Action: "ALLOW IN", From: "Anywhere"},
		{Port: "9000", Proto: "tcp", Action: "ALLOW IN", From: "Anywhere"},
		{Port: "22", Proto: "tcp", Action: "ALLOW IN", From: "Anywhere", V6: true},
	} {
		r := fw.Rules[i]
		r.Raw = ""
		if r != want {
			t.Errorf("rule %d: got %+v want %+v", i, r, want)
		}
	}
	if off := parseUFW("Status: inactive\n"); off.Active || len(off.Rules) != 0 {
		t.Errorf("inactive: %+v", off)
	}
}

func TestParseFirewalld(t *testing.T) {
	out := `public (active)
  target: default
  icmp-block-inversion: no
  interfaces: eth0
  sources:
  services: cockpit dhcpv6-client ssh
  ports: 8080/tcp 60000-61000/udp
  protocols:
  forward: yes
  masquerade: no
  forward-ports:
  source-ports:
  icmp-blocks:
  rich rules:
	rule family="ipv4" source address="10.0.0.0/8" port port="5432" protocol="tcp" accept
	rule family="ipv4" source address="192.0.2.1" reject
`
	fw := parseFirewalld(out)
	if !fw.Active || fw.Incoming != "reject" || len(fw.Rules) != 7 {
		t.Fatalf("got %+v", fw)
	}
	if r := fw.Rules[4]; r.Port != "60000:61000" || r.Proto != "udp" {
		t.Errorf("port range: %+v", r)
	}
	if r := fw.Rules[5]; r.Port != "5432" || r.Proto != "tcp" || r.From != "10.0.0.0/8" || r.Action != "ALLOW IN" {
		t.Errorf("rich rule: %+v", r)
	}
	if r := fw.Rules[6]; r.Action != "REJECT IN" || r.Port != "" {
		t.Errorf("reject rule: %+v", r)
	}
}

func TestFirewallCommandsAndRefusals(t *testing.T) {
	open := protocol.FirewallArgs{Action: "open", Port: 8080, Proto: "tcp", Source: "203.0.113.0/24"}
	if got := firewallCommands("ufw", open); !reflect.DeepEqual(got, [][]string{{"ufw", "allow", "proto", "tcp", "from", "203.0.113.0/24", "to", "any", "port", "8080"}}) {
		t.Errorf("ufw open: %v", got)
	}
	cl := protocol.FirewallArgs{Action: "close", Port: 53, Proto: "udp"}
	if got := firewallCommands("ufw", cl); !reflect.DeepEqual(got, [][]string{{"ufw", "--force", "delete", "allow", "proto", "udp", "from", "any", "to", "any", "port", "53"}}) {
		t.Errorf("ufw close: %v", got)
	}
	if got := firewallCommands("firewalld", cl); !reflect.DeepEqual(got, [][]string{{"firewall-cmd", "--remove-port=53/udp"}, {"firewall-cmd", "--permanent", "--remove-port=53/udp"}}) {
		t.Errorf("firewalld close: %v", got)
	}
	if got := firewallCommands("firewalld", open)[1][2]; got != `--add-rich-rule=rule family="ipv4" source address="203.0.113.0/24" port port="8080" protocol="tcp" accept` {
		t.Errorf("firewalld rich: %s", got)
	}
	if refuseFirewall(protocol.FirewallArgs{Action: "close", Port: 22, Proto: "tcp"}, nil) == nil {
		t.Error("closing default SSH port allowed")
	}
	if refuseFirewall(protocol.FirewallArgs{Action: "close", Port: 2222, Proto: "tcp"}, []int{2222}) == nil {
		t.Error("closing custom SSH port allowed")
	}
	if refuseFirewall(protocol.FirewallArgs{Action: "close", Port: 22, Proto: "tcp"}, []int{2222}) != nil {
		t.Error("closing 22 refused although SSH listens on 2222")
	}
	if refuseFirewall(protocol.FirewallArgs{Action: "open", Port: 22, Proto: "tcp"}, nil) != nil {
		t.Error("opening SSH refused")
	}
}

func TestParseSSHDAndApt(t *testing.T) {
	c := parseSSHD("port 22\nport 2222\naddressfamily any\npermitrootlogin without-password\npasswordauthentication yes\npubkeyauthentication yes\n")
	if !c.Installed || c.PermitRootLogin != "without-password" || c.PasswordAuth != "yes" || !reflect.DeepEqual(c.Ports, []int{22, 2222}) {
		t.Errorf("sshd: %+v", c)
	}
	apt := `Reading package lists...
Building dependency tree...
The following packages will be upgraded:
  libssl3 openssl tzdata
3 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.
Inst libssl3 [3.0.2-0ubuntu1.14] (3.0.2-0ubuntu1.15 Ubuntu:22.04/jammy-security [amd64])
Inst openssl [3.0.2-0ubuntu1.14] (3.0.2-0ubuntu1.15 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])
Inst tzdata [2024a-0ubuntu0.22.04] (2024a-0ubuntu0.22.04.1 Ubuntu:22.04/jammy-updates [all])
Conf libssl3 (3.0.2-0ubuntu1.15 Ubuntu:22.04/jammy-security [amd64])
`
	if n := countAptSecurity(apt); n != 2 {
		t.Errorf("apt security count = %d", n)
	}
}

func TestAutoUpdates(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(root+p), 0o755)
		os.WriteFile(root+p, []byte(s), 0o644)
	}
	if got := autoUpdates(root); got != "unknown" {
		t.Errorf("empty root: %q", got)
	}
	write("/etc/apt/apt.conf.d/70debconf", "")
	if got := autoUpdates(root); got != "" {
		t.Errorf("apt without unattended-upgrades: %q", got)
	}
	write("/usr/bin/unattended-upgrade", "")
	write("/etc/apt/apt.conf.d/20auto-upgrades", "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n")
	if got := autoUpdates(root); got != "unattended-upgrades" {
		t.Errorf("configured: %q", got)
	}
}

func TestParseDUAndDockerDF(t *testing.T) {
	du := "4\t/var/tmp\n1048576\t/var/lib/docker\n2097152\t/var\n12\t/var/log/apt\ndu: cannot read directory '/var/x': Permission denied\n"
	dirs := parseDU(du)
	if len(dirs) != 4 || dirs[0].Path != "/var" || dirs[0].Size != 2<<30 || dirs[1].Path != "/var/lib/docker" {
		t.Errorf("du: %+v", dirs)
	}
	many := strings.Repeat("1\t/x\n", 100)
	if len(parseDU(many)) != maxDirs {
		t.Error("du output not capped")
	}
	// Trimmed from GET /v1.43/system/df on Docker 24.
	sample := `{"LayersSize":1000,
	  "Images":[{"Id":"sha256:a","Size":600,"SharedSize":0,"Containers":1},{"Id":"sha256:b","Size":400,"SharedSize":-1,"Containers":0}],
	  "Containers":[{"Id":"c1","SizeRw":10,"State":"running"},{"Id":"c2","SizeRw":5,"State":"exited"}],
	  "Volumes":[{"Name":"pg","UsageData":{"Size":70,"RefCount":0}},{"Name":"x","UsageData":{"Size":-1,"RefCount":1}}],
	  "BuildCache":[{"ID":"b1","Size":300,"InUse":false,"Shared":false},{"ID":"b2","Size":50,"InUse":true,"Shared":false}]}`
	var raw dockerDF
	if err := json.Unmarshal([]byte(sample), &raw); err != nil {
		t.Fatal(err)
	}
	df := summarizeDF(raw)
	want := protocol.DockerDF{
		Images:     protocol.DFLine{Count: 2, Size: 1000, Reclaimable: 400},
		Containers: protocol.DFLine{Count: 2, Size: 15, Reclaimable: 5},
		Volumes:    protocol.DFLine{Count: 2, Size: 70, Reclaimable: 70},
		BuildCache: protocol.DFLine{Count: 2, Size: 350, Reclaimable: 300},
	}
	if df != want {
		t.Errorf("df: %+v", df)
	}
}

func TestComposeProjects(t *testing.T) {
	cs := []dockerContainer{
		{Names: []string{"/shop-web-1"}, State: "running", Labels: map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "web", "com.docker.compose.project.working_dir": "/srv/shop"}},
		{Names: []string{"/shop-db-1"}, State: "exited", Labels: map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "db"}},
		{Names: []string{"/standalone"}, State: "running"},
	}
	got := composeProjects(cs)
	if len(got) != 1 || got[0].WorkingDir != "/srv/shop" || len(got[0].Services) != 2 || got[0].Services[0].Service != "db" {
		t.Errorf("compose: %+v", got)
	}
}
