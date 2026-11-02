// Named volumes, command, Docker health check and the shared "orbit" network for deployed services.
package agent

import (
	"context"
	"strings"

	"orbit/internal/protocol"
)

// orbitNetwork is a user-defined bridge, so services on one server reach each other by service name.
const orbitNetwork = "orbit"

func (d *Docker) ensureNetwork(ctx context.Context) error {
	err := d.do(ctx, "POST", "/networks/create", map[string]any{"Name": orbitNetwork, "Driver": "bridge", "CheckDuplicate": true}, nil)
	if err != nil && strings.Contains(err.Error(), ": 409 ") { // already exists
		return nil
	}
	return err
}

// serviceOptions adds the optional parts of DeployArgs to a container create body.
func serviceOptions(args protocol.DeployArgs, body, hc map[string]any) error {
	var mounts []map[string]any
	for _, v := range args.Volumes {
		name, path, err := protocol.ParseVolume(v)
		if err != nil {
			return err
		}
		mounts = append(mounts, map[string]any{"Type": "volume", "Source": protocol.VolumeName(args.App, name), "Target": path})
	}
	if mounts != nil {
		hc["Mounts"] = mounts
	}
	if len(args.Command) > 0 {
		body["Cmd"] = args.Command
	}
	if args.HealthCmd != "" {
		body["Healthcheck"] = map[string]any{"Test": []string{"CMD-SHELL", args.HealthCmd}, "Interval": int64(5e9), "Timeout": int64(5e9), "Retries": 5, "StartPeriod": int64(30e9)}
	}
	hc["NetworkMode"] = orbitNetwork
	body["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{orbitNetwork: map[string]any{"Aliases": []string{args.App}}}}
	return nil
}
