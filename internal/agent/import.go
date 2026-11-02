// Importing an unmanaged container: report its settings, recreate them on deploy, and keep the original stopped.
package agent

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"

	"orbit/internal/protocol"
)

type inspectedContainer struct {
	ID     string
	Name   string
	State  struct{ Running bool }
	Config struct {
		Image      string
		Env        []string
		Cmd        []string
		Entrypoint []string
		User       string
		WorkingDir string
		Labels     map[string]string
	}
	HostConfig struct {
		Privileged   bool
		NetworkMode  string
		PidMode      string
		IpcMode      string
		CapAdd       []string
		Devices      []any
		PortBindings map[string][]struct{ HostIp, HostPort string }
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Networks map[string]struct{ Aliases []string }
	}
}

type inspectedImage struct {
	Config struct {
		Env        []string
		Cmd        []string
		Entrypoint []string
		User       string
		WorkingDir string
		Labels     map[string]string
	}
}

func (a *Agent) inspectForImport(ctx context.Context, id string) (protocol.ImportSpec, error) {
	var c inspectedContainer
	if err := a.docker.Inspect(ctx, id, &c); err != nil {
		return protocol.ImportSpec{}, err
	}
	var img inspectedImage
	if err := a.docker.do(ctx, "GET", "/images/"+url.PathEscape(c.Config.Image)+"/json", nil, &img); err != nil {
		return protocol.ImportSpec{}, fmt.Errorf("inspect image %s: %w", c.Config.Image, err)
	}
	self := ""
	if a.cfg.HostRoot != "" { // in a container, the hostname is the agent's own container ID
		self, _ = os.Hostname()
	}
	return importSpec(c, img, self), nil
}

// importSpec keeps only what the container changes from its image, and lists what Orbit can't recreate.
func importSpec(c inspectedContainer, img inspectedImage, self string) protocol.ImportSpec {
	spec := protocol.ImportSpec{Name: strings.TrimPrefix(c.Name, "/"), Image: c.Config.Image, Ports: []string{}, Env: map[string]string{},
		Compose: c.Config.Labels["com.docker.compose.project"]}
	no := func(why string) { spec.Unsupported = append(spec.Unsupported, why) }
	hc := c.HostConfig
	if self != "" && strings.HasPrefix(c.ID, self) {
		no("is the Orbit agent itself")
	}
	for k := range c.Config.Labels {
		if strings.HasPrefix(k, "orbit.") {
			no("is already managed by Orbit")
			break
		}
	}
	if hc.Privileged {
		no("runs privileged")
	}
	switch m := hc.NetworkMode; {
	case m == "host":
		no("uses the server's network directly (network_mode: host)")
	case m == "none", strings.HasPrefix(m, "container:"):
		no("shares another container's network, or has none")
	}
	if hc.PidMode == "host" || hc.IpcMode == "host" {
		no("shares the server's processes or IPC")
	}
	if len(hc.CapAdd) > 0 || len(hc.Devices) > 0 {
		no("needs extra capabilities or devices")
	}
	if !slices.Equal(c.Config.Entrypoint, img.Config.Entrypoint) {
		no("overrides the image's entrypoint")
	}
	if c.Config.User != img.Config.User || c.Config.WorkingDir != img.Config.WorkingDir {
		no("overrides the image's user or working directory")
	}

	inImage := map[string]bool{}
	for _, e := range img.Config.Env {
		inImage[e] = true
	}
	for _, e := range c.Config.Env {
		if k, v, ok := strings.Cut(e, "="); ok && !inImage[e] {
			spec.Env[k] = v
		}
	}
	if !slices.Equal(c.Config.Cmd, img.Config.Cmd) {
		spec.Command = c.Config.Cmd
	}

	ctrPorts := make([]string, 0, len(hc.PortBindings))
	for p := range hc.PortBindings {
		ctrPorts = append(ctrPorts, p)
	}
	sort.Strings(ctrPorts)
	for _, ctr := range ctrPorts {
		seen := map[string]bool{}
		for _, b := range hc.PortBindings[ctr] {
			if b.HostPort == "" || b.HostPort == "0" {
				no("publishes " + ctr + " on a random host port")
				continue
			}
			if seen[b.HostPort] { // Docker lists IPv4 and IPv6 bindings of one -p separately
				continue
			}
			seen[b.HostPort] = true
			num, proto, _ := strings.Cut(ctr, "/")
			spec.Ports = append(spec.Ports, b.HostPort+":"+num+"/"+proto)
			if ip := b.HostIp; ip != "" && ip != "0.0.0.0" && ip != "::" {
				if spec.Carry.HostIPs == nil {
					spec.Carry.HostIPs = map[string]string{}
				}
				spec.Carry.HostIPs[ctr] = ip
			}
		}
	}

	for _, m := range c.Mounts {
		switch m.Type {
		case "bind":
			spec.Carry.Mounts = append(spec.Carry.Mounts, protocol.Mount{Type: "bind", Source: m.Source, Target: m.Destination, ReadOnly: !m.RW})
		case "volume":
			spec.Carry.Mounts = append(spec.Carry.Mounts, protocol.Mount{Type: "volume", Source: m.Name, Target: m.Destination, ReadOnly: !m.RW})
		case "tmpfs":
			spec.Carry.Mounts = append(spec.Carry.Mounts, protocol.Mount{Type: "tmpfs", Target: m.Destination})
		default:
			no("has a " + m.Type + " mount")
		}
	}

	nets := make([]string, 0, len(c.NetworkSettings.Networks))
	for n := range c.NetworkSettings.Networks {
		if n != "bridge" && n != orbitNetwork && n != "host" && n != "none" {
			nets = append(nets, n)
		}
	}
	sort.Strings(nets)
	for _, n := range nets {
		// The old container's name is an alias too, so neighbours that used it still reach the service.
		aliases := []string{spec.Name}
		for _, al := range c.NetworkSettings.Networks[n].Aliases {
			if al != spec.Name && !strings.HasPrefix(c.ID, al) {
				aliases = append(aliases, al)
			}
		}
		spec.Carry.Networks = append(spec.Carry.Networks, protocol.NetworkAttach{Name: n, Aliases: aliases})
	}

	for k, v := range c.Config.Labels {
		// Compose labels would make `docker compose` treat the new container as its own.
		if img.Config.Labels[k] == v || strings.HasPrefix(k, "com.docker.compose.") || strings.HasPrefix(k, "orbit.") {
			continue
		}
		if spec.Carry.Labels == nil {
			spec.Carry.Labels = map[string]string{}
		}
		spec.Carry.Labels[k] = v
	}
	return spec
}

// applyCarried adds imported settings to a container create body built by createBody.
func applyCarried(c *protocol.Carried, body, hc map[string]any) error {
	if c == nil {
		return nil
	}
	mounts, _ := hc["Mounts"].([]map[string]any)
	for _, m := range c.Mounts {
		if m.Type != "bind" && m.Type != "volume" && m.Type != "tmpfs" {
			return fmt.Errorf("unsupported mount type %q", m.Type)
		}
		mount := map[string]any{"Type": m.Type, "Target": m.Target, "ReadOnly": m.ReadOnly}
		if m.Type != "tmpfs" {
			mount["Source"] = m.Source
		}
		mounts = append(mounts, mount)
	}
	if mounts != nil {
		hc["Mounts"] = mounts
	}
	bindings, _ := hc["PortBindings"].(map[string][]map[string]string)
	for ctr, ip := range c.HostIPs {
		for _, b := range bindings[ctr] {
			b["HostIp"] = ip
		}
	}
	labels, _ := body["Labels"].(map[string]string)
	for k, v := range c.Labels {
		if _, set := labels[k]; !set && labels != nil {
			labels[k] = v
		}
	}
	return nil
}

// joinCarriedNetworks connects a created container to the imported container's other networks.
func (d *Docker) joinCarriedNetworks(ctx context.Context, c *protocol.Carried, id string) error {
	if c == nil {
		return nil
	}
	for _, n := range c.Networks {
		err := d.do(ctx, "POST", "/networks/"+url.PathEscape(n.Name)+"/connect",
			map[string]any{"Container": id, "EndpointConfig": map[string]any{"Aliases": n.Aliases}}, nil)
		if err != nil {
			return fmt.Errorf("join network %s: %w", n.Name, err)
		}
	}
	return nil
}

// retire keeps an imported container as a stopped fallback: renamed, and never restarted by Docker.
func (d *Docker) retire(ctx context.Context, id, name string) error {
	if strings.HasSuffix(name, "-before-orbit") { // already retired by an earlier attempt
		return nil
	}
	if err := d.do(ctx, "POST", "/containers/"+url.PathEscape(id)+"/update", map[string]any{"RestartPolicy": map[string]string{"Name": "no"}}, nil); err != nil {
		return err
	}
	return d.do(ctx, "POST", "/containers/"+url.PathEscape(id)+"/rename?name="+url.QueryEscape(name+"-before-orbit"), nil, nil)
}
