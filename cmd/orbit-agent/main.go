// orbit-agent connects a server to an Orbit control plane. Configuration is environment-only.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"orbit/internal/agent"
)

var version = "dev" // set with -ldflags "-X main.version=..."

func main() {
	if len(os.Args) == 3 && os.Args[1] == "backup-receive" { // forced SSH command of the orbit-backups user
		os.Exit(agent.BackupReceive(os.Args[2]))
	}
	if len(os.Args) == 2 && os.Args[1] == "disconnect" {
		os.Exit(agent.Disconnect(env("ORBIT_STATE", "/var/lib/orbit-agent/state.json"), os.Stdout))
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := agent.Config{
		URL:       strings.TrimSuffix(os.Getenv("ORBIT_URL"), "/"),
		Token:     os.Getenv("ORBIT_TOKEN"),
		StateFile: env("ORBIT_STATE", "/var/lib/orbit-agent/state.json"),
		HostRoot:  os.Getenv("ORBIT_HOST_ROOT"),
		Socket:    env("DOCKER_SOCKET", "/var/run/docker.sock"),
		Version:   version,
		Region:    os.Getenv("ORBIT_REGION"),
		NomadAddr: os.Getenv("NOMAD_ADDR"),
		NomadTok:  os.Getenv("NOMAD_TOKEN"),
	}
	if cfg.URL == "" {
		log.Error("ORBIT_URL is required")
		os.Exit(2)
	}
	if !strings.HasPrefix(cfg.URL, "https://") && os.Getenv("ORBIT_INSECURE") != "1" {
		log.Error("ORBIT_URL must be https:// (set ORBIT_INSECURE=1 for local development only)")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := agent.New(cfg, log).Run(ctx); err != nil {
		log.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
