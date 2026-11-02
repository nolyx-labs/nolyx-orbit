package agent

import (
	"reflect"
	"strings"
	"testing"

	"orbit/internal/protocol"
)

func TestParseAptUpgradable(t *testing.T) {
	// Ubuntu 22.04 and Debian 12, including a second architecture and an "N:" notice.
	out := `Listing... Done
libssl3/jammy-updates,jammy-security 3.0.2-0ubuntu1.15 amd64 [upgradable from: 3.0.2-0ubuntu1.14]
libssl3/jammy-updates,jammy-security 3.0.2-0ubuntu1.15 i386 [upgradable from: 3.0.2-0ubuntu1.14]
tzdata/jammy-updates 2024a-0ubuntu0.22.04 all [upgradable from: 2023c-0ubuntu0.22.04.2]
libc6/stable-security 2.36-9+deb12u4 amd64 [upgradable from: 2.36-9+deb12u3]
N: There is 1 additional version. Please use the '-a' switch to see it
`
	want := []protocol.OSPackage{
		{Name: "libssl3", Installed: "3.0.2-0ubuntu1.14", Available: "3.0.2-0ubuntu1.15", Security: true},
		{Name: "tzdata", Installed: "2023c-0ubuntu0.22.04.2", Available: "2024a-0ubuntu0.22.04"},
		{Name: "libc6", Installed: "2.36-9+deb12u3", Available: "2.36-9+deb12u4", Security: true},
	}
	if got := parseAptUpgradable(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestParseDnf(t *testing.T) {
	// dnf4 on Rocky 9: a wrapped long name, an epoch, two arches and the obsoletes section.
	check := `
bash.x86_64                              5.1.8-9.el9                    baseos
NetworkManager-cloud-setup-extra-long-name.x86_64
                                         1:1.46.0-8.el9_4               baseos
openssl-libs.x86_64                      1:3.0.7-27.el9                 baseos
openssl-libs.i686                        1:3.0.7-27.el9                 baseos
Obsoleting Packages
grub2-tools.x86_64                       1:2.06-80.el9                  baseos
    grub2-tools.x86_64                   1:2.06-77.el9                  @baseos
`
	pkgs := parseDnfCheckUpdate(check)
	names := []string{}
	for _, p := range pkgs {
		names = append(names, p.Name+"="+p.Available)
	}
	if want := "bash=5.1.8-9.el9 NetworkManager-cloud-setup-extra-long-name=1:1.46.0-8.el9_4 openssl-libs=1:3.0.7-27.el9"; strings.Join(names, " ") != want {
		t.Fatalf("check-update: %v", names)
	}

	installed := parseRpmVersions("bash 5.1.8-6.el9_1\nopenssl-libs 1:3.0.7-24.el9\nopenssl-libs 1:3.0.7-24.el9\n")
	if installed["bash"] != "5.1.8-6.el9_1" || installed["openssl-libs"] != "1:3.0.7-24.el9" {
		t.Errorf("rpm: %v", installed)
	}

	// dnf4 (RHEL advisory first) and dnf5 (name, type, severity, package, issued) layouts.
	markDnfSecurity(pkgs, `RHSA-2024:2447 Important/Sec. openssl-libs-1:3.0.7-27.el9.x86_64
FEDORA-2024-1a2b3c4d5e security Moderate NetworkManager-cloud-setup-extra-long-name-1:1.46.0-8.el9_4.x86_64 2024-05-01 10:00:00
`)
	if pkgs[0].Security || !pkgs[1].Security || !pkgs[2].Security {
		t.Errorf("security flags: %+v", pkgs)
	}
}

func TestRPMNames(t *testing.T) {
	for in, want := range map[string]string{
		"openssl-libs-1:3.0.7-27.el9.x86_64": "openssl-libs",
		"bash-0:5.1.8-6.el9_1.*":             "bash",
		"kernel-core-5.14.0-427.el9.noarch":  "kernel-core",
		"Important/Sec.":                     "",
	} {
		if got := rpmName(in); got != want {
			t.Errorf("rpmName(%q) = %q, want %q", in, got, want)
		}
	}
	locks := parseVersionlock("# Added by 'versionlock add' command on 2024-05-01\nbash-0:5.1.8-6.el9_1.*\n\nPackage name: kernel-core\nevr = 5.14.0-427.el9\n")
	if !reflect.DeepEqual(locks, []string{"bash", "kernel-core"}) {
		t.Errorf("versionlock: %v", locks)
	}
	if !dnfRebootRequired("Core libraries or services have been updated since boot-up:\n  * kernel\n\nReboot is required to fully utilize these updates.\n") ||
		dnfRebootRequired("No core libraries or services have been updated since boot-up.\nReboot should not be necessary.\n") ||
		dnfRebootRequired("No such command: needs-restarting.") {
		t.Error("needs-restarting")
	}
}

func TestInstallCommands(t *testing.T) {
	pending := protocol.OSCheckResult{Held: []string{"docker-ce"}, Packages: []protocol.OSPackage{
		{Name: "openssl", Security: true}, {Name: "tzdata"}, {Name: "docker-ce", Security: true},
	}}
	cases := []struct {
		mgr  string
		args protocol.OSInstallArgs
		want string // commands joined by " | "
		err  string
	}{
		{"apt", protocol.OSInstallArgs{Scope: "security"}, "apt-get install -y --only-upgrade -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold openssl", ""},
		{"apt", protocol.OSInstallArgs{Scope: "all"}, "apt-get install -y --only-upgrade -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold openssl tzdata", ""},
		{"apt", protocol.OSInstallArgs{Scope: "packages", Packages: []protocol.OSPackageSpec{{Name: "tzdata"}, {Name: "openssl", Version: "3.0.2-0ubuntu1.10"}}},
			"apt-get install -y --only-upgrade -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold --allow-downgrades tzdata openssl=3.0.2-0ubuntu1.10", ""},
		{"apt", protocol.OSInstallArgs{Scope: "packages", Packages: []protocol.OSPackageSpec{{Name: "docker-ce"}}}, "", "held"},
		{"apt", protocol.OSInstallArgs{Scope: "packages", Packages: []protocol.OSPackageSpec{{Name: "-oAPT::Update=x"}}}, "", "invalid"},
		{"apt", protocol.OSInstallArgs{Scope: "packages", Packages: []protocol.OSPackageSpec{{Name: "tzdata", Version: "1; rm -rf /"}}}, "", "invalid"},
		{"dnf", protocol.OSInstallArgs{Scope: "security"}, "dnf upgrade -y --security", ""},
		{"dnf", protocol.OSInstallArgs{Scope: "packages", Packages: []protocol.OSPackageSpec{{Name: "bash"}, {Name: "openssl-libs", Version: "1:3.0.7-24.el9"}}},
			"dnf upgrade -y bash | dnf install -y openssl-libs-1:3.0.7-24.el9", ""},
	}
	for _, c := range cases {
		cmds, err := installCommands(c.mgr, c.args, pending)
		var parts []string
		for _, cmd := range cmds {
			parts = append(parts, strings.Join(cmd, " "))
		}
		if got := strings.Join(parts, " | "); got != c.want || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s %+v: got %q, %v", c.mgr, c.args, got, err)
		}
	}
}
