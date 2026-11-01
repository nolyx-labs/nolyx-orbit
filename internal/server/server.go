// Package server is the Orbit control plane: API, agent gateway, realtime and background worker.
package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/hkdf"
)

type Config struct {
	DatabaseURL          string
	AppURL               string
	EncryptionKey        string // 32 bytes, hex or base64
	AllowSignup          bool
	AllowPrivateWebhooks bool
	AgentDist            string // directory holding orbit-agent-linux-{amd64,arm64}
	Web                  fs.FS  // built SPA

	SMTPHost, SMTPPort, SMTPUser, SMTPPass, SMTPFrom string
}

type Server struct {
	cfg    Config
	db     *pgxpool.Pool
	aead   cipher.AEAD
	signer ed25519.PrivateKey // command signing; agents pin the public key at enrollment
	hub    *Hub
	log    *slog.Logger

	attempts attempts // failed sign-ins etc., see limit.go
}

//go:embed migrations/*.sql
var migrations embed.FS

func New(ctx context.Context, cfg Config, log *slog.Logger) (*Server, error) {
	key, err := decodeKey(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	seed := make([]byte, ed25519.SeedSize)
	io.ReadFull(hkdf.New(sha256.New, key, nil, []byte("orbit command signing v1")), seed)
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	s := &Server{cfg: cfg, db: db, aead: aead, signer: ed25519.NewKeyFromSeed(seed), hub: newHub(), log: log}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Single instance: nothing is connected at boot. See docs/ARCHITECTURE.md §7 for multi-instance.
	_, err = db.Exec(ctx, `update servers set status = 'offline' where status = 'online'`)
	return s, err
}

func decodeKey(k string) ([]byte, error) {
	if b, err := hex.DecodeString(k); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(k); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("ENCRYPTION_KEY must be 32 bytes, hex or base64 encoded (openssl rand -hex 32)")
}

func (s *Server) migrate(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `create table if not exists schema_migrations (name text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	for _, name := range names {
		var done bool
		s.db.QueryRow(ctx, `select exists(select 1 from schema_migrations where name = $1)`, name).Scan(&done)
		if done {
			continue
		}
		sql, _ := migrations.ReadFile(name)
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `insert into schema_migrations (name) values ($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		s.log.Info("applied migration", "name", name)
	}
	return nil
}

func (s *Server) Close() { s.db.Close() }
