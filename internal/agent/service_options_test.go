package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"orbit/internal/protocol"
)

func TestCreateBodyVolumesCommandNetwork(t *testing.T) {
	body, err := createBody(protocol.DeployArgs{App: "blog-db", Image: "mariadb:11.8.9", Volumes: []string{"data:/var/lib/mysql"},
		Command: []string{"--max-connections=50"}, HealthCmd: "healthcheck.sh --connect"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	for _, want := range []string{
		`"Mounts":[{"Source":"orbit-blog-db-data","Target":"/var/lib/mysql","Type":"volume"}]`,
		`"Cmd":["--max-connections=50"]`,
		`"Test":["CMD-SHELL","healthcheck.sh --connect"]`,
		`"NetworkMode":"orbit"`,
		`"EndpointsConfig":{"orbit":{"Aliases":["blog-db"]}}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("create body lacks %s:\n%s", want, b)
		}
	}
	if _, err := createBody(protocol.DeployArgs{App: "x", Image: "x:1", Volumes: []string{"/etc:/host"}}); err == nil {
		t.Error("host path accepted as a volume")
	}
	plain, _ := createBody(protocol.DeployArgs{App: "x", Image: "x:1"})
	if _, ok := plain["Cmd"]; ok {
		t.Error("empty command overrides the image CMD")
	}
}
