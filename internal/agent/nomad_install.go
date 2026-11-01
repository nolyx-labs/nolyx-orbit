// Installing Nomad on a server as a container.
package agent

import (
	"context"
	"fmt"
	"orbit/internal/protocol"
	"regexp"
	"strings"
	"time"
)

var hclSafe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// nomadInstall runs Nomad as a container on this server (server+client or client only).
// shortcut: Nomad-in-Docker supports the docker driver only (no exec/raw_exec isolation); install natively for those.
func (a *Agent) nomadInstall(ctx context.Context, args protocol.NomadInstallArgs, progress func(protocol.Progress)) error {
	for _, v := range append([]string{args.Datacenter, args.Region, args.Version, args.AdvertiseIP}, args.Join...) {
		if !hclSafe.MatchString(v) {
			return fmt.Errorf("invalid argument %q", v)
		}
	}
	if a.nomad.version(ctx) != "" {
		return fmt.Errorf("a Nomad agent is already running on this server")
	}
	quoted := func(xs []string) string {
		q := make([]string, len(xs))
		for i, x := range xs {
			q[i] = `"` + x + `"`
		}
		return strings.Join(q, ", ")
	}
	cfg := fmt.Sprintf(`datacenter = "%s"
region = "%s"
data_dir = "/opt/nomad/data"
bind_addr = "0.0.0.0"
advertise {
  http = "%[3]s"
  rpc  = "%[3]s"
  serf = "%[3]s"
}
client {
  enabled = true
  servers = [%[4]s]
}
plugin "docker" {
  config {
    volumes { enabled = true }
  }
}
`, args.Datacenter, args.Region, args.AdvertiseIP, quoted(args.Join))
	if args.Server {
		cfg += fmt.Sprintf("server {\n  enabled = true\n  bootstrap_expect = %d\n  server_join { retry_join = [%s] }\n}\n", max(args.BootstrapExpect, 1), quoted(args.Join))
	}
	step := stepper(progress)
	image := "hashicorp/nomad:" + args.Version
	if err := step("Pulling Nomad", func() error { return a.docker.EnsureImage(ctx, image) }); err != nil {
		return err
	}
	var id string
	if err := step("Creating Nomad agent", func() (err error) {
		a.docker.Action(ctx, protocol.ActionContainerRemove, "orbit-nomad")
		id, err = a.docker.CreateRaw(ctx, "orbit-nomad", map[string]any{
			"Image":  image,
			"Cmd":    []string{"agent", "-config=/etc/nomad.d/orbit.hcl"},
			"Labels": map[string]string{"orbit.managed": "true", "orbit.nomad": "true"},
			"Env":    []string{"NOMAD_SKIP_DOCKER_IMAGE_WARN=1"},
			"HostConfig": map[string]any{
				// the client manages cgroups for tasks, so it needs the host's cgroup namespace
				"NetworkMode": "host", "Privileged": true, "CgroupnsMode": "host", "RestartPolicy": map[string]string{"Name": "unless-stopped"},
				// the data dir must have the same path inside and outside so task bind mounts resolve
				"Binds": []string{"/var/run/docker.sock:/var/run/docker.sock", "/opt/nomad/data:/opt/nomad/data", "/sys/fs/cgroup:/sys/fs/cgroup:rw"},
			},
		})
		if err == nil {
			err = a.docker.Upload(ctx, id, "/", map[string]string{"etc/nomad.d/orbit.hcl": cfg})
		}
		return err
	}); err != nil {
		return err
	}
	if err := step("Starting Nomad", func() error { return a.docker.Action(ctx, protocol.ActionContainerStart, id) }); err != nil {
		return err
	}
	return step("Waiting for Nomad API", func() error {
		for i := 0; i < 40; i++ {
			time.Sleep(3 * time.Second)
			if a.nomad.version(ctx) != "" {
				return nil
			}
		}
		out, _ := a.docker.rawLogs(ctx, id)
		return fmt.Errorf("Nomad did not become ready:\n%s", tail(out, 15))
	})
}
