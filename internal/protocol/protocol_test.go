package protocol

import "testing"

func TestParsePort(t *testing.T) {
	cases := map[string][2]string{"3000": {"3000", "3000/tcp"}, "8080:3000": {"8080", "3000/tcp"}, "53:53/udp": {"53", "53/udp"}}
	for in, want := range cases {
		h, c, err := ParsePort(in)
		if err != nil || h != want[0] || c != want[1] {
			t.Errorf("ParsePort(%q) = %q %q %v", in, h, c, err)
		}
	}
	for _, bad := range []string{"", "1:2:3", "80/sctp", ":80", "99999", "abc"} {
		if _, _, err := ParsePort(bad); err == nil {
			t.Errorf("ParsePort(%q) accepted", bad)
		}
	}
}
