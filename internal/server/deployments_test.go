package server

import (
	"encoding/json"
	"testing"
)

func TestGitRemoteAndNomadJob(t *testing.T) {
	if got := gitRemote("https://github.com/a/b", "tok"); got != "https://x-access-token:tok@github.com/a/b.git" {
		t.Fatalf("gitRemote = %s", got)
	}
	if m := githubRepoRe.FindStringSubmatch("https://github.com/traefik/whoami.git"); m == nil || m[1] != "traefik" || m[2] != "whoami" {
		t.Fatalf("repo regexp = %v", m)
	}
	raw, err := nomadJob("api", "nginx:1", []string{"8080:80"}, map[string]string{"A": "1"}, "/health", 3, 0.5, 512)
	if err != nil {
		t.Fatal(err)
	}
	var job struct {
		ID         string
		TaskGroups []struct {
			Count    int
			Networks []struct{ ReservedPorts []struct{ Value, To int } }
			Services []struct{ Checks []struct{ Path string } }
			Tasks    []struct{ Resources struct{ CPU, MemoryMB int } }
		}
		Update struct{ HealthCheck string }
	}
	json.Unmarshal(raw, &job)
	tg := job.TaskGroups[0]
	if job.ID != "api" || tg.Count != 3 || tg.Networks[0].ReservedPorts[0].Value != 8080 || tg.Networks[0].ReservedPorts[0].To != 80 ||
		tg.Services[0].Checks[0].Path != "/health" || tg.Tasks[0].Resources.CPU != 500 || job.Update.HealthCheck != "checks" {
		t.Fatalf("unexpected job: %s", raw)
	}
}
