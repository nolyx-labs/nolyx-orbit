// orbit is the control plane: API, web UI, agent gateway and worker in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"orbit/internal/server"
	"orbit/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := server.Config{
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		AppURL:               strings.TrimSuffix(env("APP_URL", "http://localhost:8080"), "/"),
		EncryptionKey:        os.Getenv("ENCRYPTION_KEY"),
		AllowSignup:          os.Getenv("ALLOW_SIGNUP") == "true",
		AllowPrivateWebhooks: os.Getenv("ALLOW_PRIVATE_WEBHOOKS") == "true",
		AgentDist:            env("AGENT_DIST", "/opt/orbit/agents"),
		Web:                  web.Dist(),
		SMTPHost:             os.Getenv("SMTP_HOST"),
		SMTPPort:             env("SMTP_PORT", "587"),
		SMTPUser:             os.Getenv("SMTP_USERNAME"),
		SMTPPass:             os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:             os.Getenv("SMTP_FROM"),
		OIDCIssuer:           os.Getenv("OIDC_ISSUER"),
		OIDCClientID:         os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:     os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCName:             env("OIDC_NAME", "SSO"),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// orbit reset-password <email>: account recovery for an operator, e.g. a lost 2FA device and no email set up.
	if len(os.Args) == 3 && os.Args[1] == "reset-password" {
		link, err := server.RecoveryLink(ctx, cfg, os.Args[2])
		if err != nil {
			log.Error("reset-password failed", "err", err)
			os.Exit(1)
		}
		fmt.Println("Open this link within an hour to set a new password. It also turns off two-factor authentication:\n" + link)
		return
	}

	s, err := server.New(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer s.Close()
	go s.RunWorker(ctx)

	srv := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("orbit listening", "addr", srv.Addr, "app_url", cfg.AppURL)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
