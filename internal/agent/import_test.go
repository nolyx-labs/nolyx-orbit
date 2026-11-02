package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"orbit/internal/protocol"
)

func TestImportSpec(t *testing.T) {
	var c inspectedContainer
	var img inspectedImage
	json.Unmarshal([]byte(`{"ID":"abc123def456789","Name":"/shop_web_1",
		"Config":{"Image":"shop/web:2","Env":["PATH=/usr/bin","DATABASE_URL=postgres://db/shop"],"Cmd":["serve","--fast"],
		  "Labels":{"com.docker.compose.project":"shop","traefik.enable":"true","maintainer":"x"}},
		"HostConfig":{"NetworkMode":"shop_default","PortBindings":{"8080/tcp":[{"HostIp":"","HostPort":"80"},{"HostIp":"::","HostPort":"80"}],
		  "5432/tcp":[{"HostIp":"127.0.0.1","HostPort":"5432"}]}},
		"Mounts":[{"Type":"bind","Source":"/srv/shop/uploads","Destination":"/app/uploads","RW":true},
		  {"Type":"volume","Name":"shop_data","Destination":"/data","RW":false}],
		"NetworkSettings":{"Networks":{"shop_default":{"Aliases":["web","abc123def456"]}}}}`), &c)
	json.Unmarshal([]byte(`{"Config":{"Env":["PATH=/usr/bin"],"Cmd":["serve"],"Labels":{"maintainer":"x"}}}`), &img)

	s := importSpec(c, img, "")
	if len(s.Unsupported) > 0 {
		t.Fatalf("unexpected unsupported: %v", s.Unsupported)
	}
	if s.Name != "shop_web_1" || s.Compose != "shop" || len(s.Env) != 1 || s.Env["DATABASE_URL"] == "" {
		t.Errorf("name/compose/env: %+v", s)
	}
	if !slices.Equal(s.Command, []string{"serve", "--fast"}) {
		t.Errorf("command: %v", s.Command)
	}
	if !slices.Equal(s.Ports, []string{"5432:5432/tcp", "80:8080/tcp"}) || s.Carry.HostIPs["5432/tcp"] != "127.0.0.1" {
		t.Errorf("ports: %v %v", s.Ports, s.Carry.HostIPs)
	}
	if len(s.Carry.Mounts) != 2 || s.Carry.Mounts[1] != (protocol.Mount{Type: "volume", Source: "shop_data", Target: "/data", ReadOnly: true}) {
		t.Errorf("mounts: %+v", s.Carry.Mounts)
	}
	if len(s.Carry.Networks) != 1 || !slices.Equal(s.Carry.Networks[0].Aliases, []string{"shop_web_1", "web"}) {
		t.Errorf("networks: %+v", s.Carry.Networks)
	}
	if len(s.Carry.Labels) != 1 || s.Carry.Labels["traefik.enable"] != "true" {
		t.Errorf("labels: %v", s.Carry.Labels)
	}

	// The carried settings reach the container create body.
	body, err := createBody(protocol.DeployArgs{App: "web", Image: s.Image, Ports: s.Ports, Carry: &s.Carry})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	for _, want := range []string{`"5432/tcp":[{"HostIp":"127.0.0.1","HostPort":"5432"}]`, `"Source":"/srv/shop/uploads"`,
		`"traefik.enable":"true"`, `"orbit.app":"web"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("create body lacks %s:\n%s", want, b)
		}
	}

	c.HostConfig.Privileged, c.HostConfig.NetworkMode = true, "host"
	c.Config.Labels["orbit.app"] = "x"
	if got := importSpec(c, img, "abc123"); len(got.Unsupported) != 4 {
		t.Errorf("want privileged, host network, managed and self refused, got %v", got.Unsupported)
	}
}
