package agent

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"orbit/internal/protocol"
)

var appName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// deploy replaces the app's container.
//   - direct mode: the old container is stopped first (it owns the host port) and restarted on failure.
//   - proxy mode: the new container's first port isn't published; once it's healthy on its bridge IP the
//     agent's proxy switches traffic to it and the old container is drained and removed. No downtime.
func (a *Agent) deploy(ctx context.Context, id string, args protocol.DeployArgs, progress func(protocol.Progress)) (err error) {
	if !appName.MatchString(args.App) || args.Image == "" || (args.Proxy && len(args.Ports) == 0) {
		return fmt.Errorf("invalid app name, image or ports")
	}
	step := stepper(progress)
	log := func(format string, v ...any) { progress(protocol.Progress{Line: fmt.Sprintf(format, v...)}) }

	// --- server backups (server_backup.go): snapshot the service's volumes before anything is replaced ---
	if args.Snapshot != nil {
		log("taking a pre-deploy snapshot of %s's volumes", args.App)
		if err := a.preDeploySnapshot(ctx, args.App, *args.Snapshot); err != nil {
			return fmt.Errorf("pre-deploy snapshot failed, so nothing was deployed: %w", err)
		}
	}
	// --- end server backups ---

	if args.Build != nil {
		err = step("Building image", func() error {
			log("building %s from %s@%s", args.Image, redact(args.Build.Remote), args.Build.Ref)
			return a.docker.Build(ctx, *args.Build, args.Image, func(l string) { log("%s", redact(l)) })
		})
	} else {
		err = step("Pulling image", func() error {
			log("pulling %s", args.Image)
			err := a.docker.Pull(ctx, args.Image)
			if err != nil && a.docker.do(ctx, "GET", "/images/"+args.Image+"/json", nil, nil) == nil {
				log("pull failed (%v); using the local image (e.g. a release built on this server)", err)
				return nil
			}
			return err
		})
	}
	if err != nil {
		return err
	}

	old, err := a.docker.list(ctx, `{"label":["orbit.app=`+args.App+`"]}`)
	if err != nil {
		return err
	}
	var stopped []string
	if !args.Proxy {
		if err := step("Stopping previous", func() error {
			a.proxy.remove(args.App)
			for _, c := range old {
				if c.State == "running" {
					log("stopping %s", c.ID[:12])
					if err := a.docker.Action(ctx, protocol.ActionContainerStop, c.ID); err != nil {
						return err
					}
					stopped = append(stopped, c.ID)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	var newID string
	defer func() {
		if err == nil {
			return
		}
		log("rolling back")
		bg := context.WithoutCancel(ctx)
		if newID != "" {
			a.docker.Action(bg, protocol.ActionContainerRemove, newID)
		}
		for _, cid := range stopped {
			if rerr := a.docker.Action(bg, protocol.ActionContainerStart, cid); rerr != nil {
				log("restart previous %s failed: %v", cid[:12], rerr)
			}
		}
		a.proxy.restore(bg, a.docker)
	}()

	name := fmt.Sprintf("orbit-%s-%s", args.App, strings.ReplaceAll(id, "-", "")[:8])
	if err = step("Creating container", func() error {
		body, err := createBody(args)
		if err != nil {
			return err
		}
		if err := a.docker.ensureNetwork(ctx); err != nil {
			return err
		}
		newID, err = a.docker.CreateRaw(ctx, name, body)
		log("created %s", name)
		return err
	}); err != nil {
		return err
	}
	if err = step("Starting container", func() error { return a.docker.Action(ctx, protocol.ActionContainerStart, newID) }); err != nil {
		return err
	}
	healthAddr := ""
	if len(args.Ports) > 0 {
		host, _, _ := protocol.ParsePort(args.Ports[0])
		healthAddr = "127.0.0.1:" + host
	}
	if args.Proxy {
		if healthAddr, err = a.upstreamAddr(ctx, newID, args.Ports[0]); err != nil {
			return err
		}
	}
	if err = step("Health check", func() error { return a.healthCheck(ctx, newID, healthAddr, args.HealthPath, args.HealthCmd != "", log) }); err != nil {
		return err
	}
	if args.Proxy {
		listen, _, _ := protocol.ParsePort(args.Ports[0])
		if err = step("Switching traffic", func() error {
			for _, c := range old { // a container from a direct-mode deploy still owns the port: hand it over
				if c.State == "running" && c.Labels["orbit.proxy.listen"] == "" {
					log("stopping direct-mode container %s to free port %s", c.ID[:12], listen)
					if err := a.docker.Action(ctx, protocol.ActionContainerStop, c.ID); err != nil {
						return err
					}
					stopped = append(stopped, c.ID)
				}
			}
			log("routing :%s → %s", listen, healthAddr)
			return a.proxy.route(args.App, listen, healthAddr)
		}); err != nil {
			return err
		}
	}
	return step("Cleaning up", func() error {
		if args.Proxy && len(old) > 0 {
			log("draining previous containers for 5s")
			time.Sleep(5 * time.Second)
		}
		for _, c := range old {
			if err := a.docker.Action(ctx, protocol.ActionContainerRemove, c.ID); err != nil {
				log("remove %s: %v", c.ID[:12], err)
			}
		}
		return nil
	})
}

func createBody(args protocol.DeployArgs) (map[string]any, error) {
	env := make([]string, 0, len(args.Env))
	for k, v := range args.Env {
		env = append(env, k+"="+v)
	}
	labels := map[string]string{"orbit.app": args.App, "orbit.managed": "true"}
	exposed := map[string]struct{}{}
	bindings := map[string][]map[string]string{}
	for i, p := range args.Ports {
		host, ctr, err := protocol.ParsePort(p)
		if err != nil {
			return nil, err
		}
		exposed[ctr] = struct{}{}
		if i == 0 && args.Proxy {
			// The agent's proxy owns the public port and reaches the container on its bridge IP.
			labels["orbit.proxy.listen"] = host
			labels["orbit.proxy.target"] = ctr
			continue
		}
		bindings[ctr] = append(bindings[ctr], map[string]string{"HostPort": host})
	}
	hc := resources(args.CPUs, args.MemoryMB)
	hc["PortBindings"] = bindings
	hc["RestartPolicy"] = map[string]string{"Name": "unless-stopped"}
	body := map[string]any{"Image": args.Image, "Env": env, "Labels": labels, "ExposedPorts": exposed, "HostConfig": hc}
	return body, serviceOptions(args, body, hc)
}

// upstreamAddr is the container's bridge IP and target port, used by the proxy.
func (a *Agent) upstreamAddr(ctx context.Context, id, port string) (string, error) {
	_, ctr, _ := protocol.ParsePort(port)
	var insp struct {
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
	}
	if err := a.docker.Inspect(ctx, id, &insp); err != nil {
		return "", err
	}
	for _, n := range insp.NetworkSettings.Networks {
		if n.IPAddress != "" {
			return n.IPAddress + ":" + strings.Split(ctr, "/")[0], nil
		}
	}
	return "", fmt.Errorf("container has no network address")
}

func (a *Agent) healthCheck(ctx context.Context, cid, addr, path string, dockerHealth bool, log func(string, ...any)) error {
	url := ""
	if path != "" && addr != "" {
		url = "http://" + addr + "/" + strings.TrimPrefix(path, "/")
	}
	deadline := time.Now().Add(90 * time.Second)
	if dockerHealth { // databases initialising on first start can take minutes
		deadline = time.Now().Add(5 * time.Minute)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		var insp struct {
			State struct {
				Running  bool
				Status   string
				ExitCode int
				Health   *struct{ Status string }
			}
		}
		if err := a.docker.Inspect(ctx, cid, &insp); err != nil {
			return err
		}
		if !insp.State.Running {
			out, _ := a.docker.rawLogs(ctx, cid)
			return fmt.Errorf("container exited (status %s, code %d):\n%s", insp.State.Status, insp.State.ExitCode, tail(out, 10))
		}
		if dockerHealth {
			if insp.State.Health == nil || insp.State.Health.Status != "healthy" {
				log("waiting for the container's health check")
				continue
			}
			log("container health check passed")
		}
		if url == "" {
			log("no health check path configured; container running")
			return nil
		}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			log("GET %s → %d", url, resp.StatusCode)
			if resp.StatusCode < 400 {
				return nil
			}
		} else {
			log("GET %s → %v", url, err)
		}
	}
	return fmt.Errorf("health check did not pass within 90s")
}

var credsInURL = regexp.MustCompile(`://[^/@\s]+@`)

// redact strips credentials from git URLs before they reach logs.
func redact(s string) string { return credsInURL.ReplaceAllString(s, "://***@") }
