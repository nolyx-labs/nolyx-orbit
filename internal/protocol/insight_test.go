package protocol

import "testing"

func TestServiceArgsValidate(t *testing.T) {
	for _, ok := range []string{"nginx.service", "getty@tty1.service", `systemd-fsck@dev-disk-by\x2duuid-1234.service`, "docker.service"} {
		if err := (ServiceArgs{Unit: ok, Action: "restart"}).Validate(); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "nginx", "-H.service", "--now.service", "a b.service", "*.service", "nginx.socket", "x;rm.service", "../x.service"} {
		if (ServiceArgs{Unit: bad, Action: "restart"}).Validate() == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if (ServiceArgs{Unit: "nginx.service", Action: "mask"}).Validate() == nil {
		t.Error("action mask accepted")
	}
}

func TestFirewallArgsValidate(t *testing.T) {
	for in, want := range map[string]string{"": "", "203.0.113.7": "203.0.113.7/32", "10.1.2.3/8": "10.0.0.0/8", "2001:db8::1": "2001:db8::1/128"} {
		a := FirewallArgs{Action: "open", Port: 443, Proto: "tcp", Source: in}
		if err := a.Validate(); err != nil || a.Source != want {
			t.Errorf("source %q: %q %v", in, a.Source, err)
		}
	}
	for _, bad := range []FirewallArgs{
		{Action: "open", Port: 0, Proto: "tcp"},
		{Action: "open", Port: 70000, Proto: "tcp"},
		{Action: "open", Port: 80, Proto: "sctp"},
		{Action: "drop", Port: 80, Proto: "tcp"},
		{Action: "open", Port: 80, Proto: "tcp", Source: "anywhere"},
		{Action: "open", Port: 80, Proto: "tcp", Source: `1.2.3.4" accept`},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
