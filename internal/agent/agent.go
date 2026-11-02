// Package agent is orbit-agent: it dials out to the control plane, reports metrics, containers,
// databases and logs, and executes an allowlisted set of signed commands.
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"orbit/internal/protocol"
)

type Config struct {
	URL       string // control plane base URL, e.g. https://orbit.example.com
	Token     string // enrollment token, only needed on first start
	StateFile string
	HostRoot  string
	Socket    string
	Version   string
	Region    string
	NomadAddr string
	NomadTok  string
}

type state struct {
	ServerID   string `json:"server_id"`
	Secret     string `json:"secret"`
	SigningKey []byte `json:"signing_key"`
}

type Agent struct {
	cfg    Config
	host   *Host
	docker *Docker
	state  state
	log    *slog.Logger
	dbs    *dbMonitor
	proxy  *proxy
	nomad  *nomadClient

	mu      sync.Mutex
	conn    *websocket.Conn
	backlog []protocol.Metrics // samples kept while disconnected
	seen    *seenSet
	actions *actionLog
}

const maxBacklog = 360 // 1h at 10s

func New(cfg Config, log *slog.Logger) *Agent {
	a := &Agent{cfg: cfg, host: &Host{Root: cfg.HostRoot}, docker: NewDocker(cfg.Socket), log: log, seen: newSeenSet(4096), actions: newActionLog(cfg.StateFile)}
	a.dbs = newDBMonitor(log)
	a.proxy = newProxy(log)
	a.nomad = newNomadClient(cfg.NomadAddr, cfg.NomadTok)
	return a
}

func (a *Agent) Run(ctx context.Context) error {
	if err := a.loadOrEnroll(ctx); err != nil {
		return err
	}
	a.proxy.restore(ctx, a.docker)
	go a.collect(ctx)
	go a.shipLogs(ctx)
	go a.reportDatabases(ctx)
	go a.reportAppMetrics(ctx)
	go a.reportNomad(ctx)
	go a.reportInsight(ctx)
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx)
		if errors.Is(err, errUnauthorized) {
			return fmt.Errorf("control plane rejected agent credentials (server removed?): delete %s and re-enroll", a.cfg.StateFile)
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1))
		a.log.Warn("disconnected", "err", err, "retry_in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return nil
}

func (a *Agent) hello(ctx context.Context) protocol.Hello {
	dv, _ := a.docker.Version(ctx)
	h := a.host.Hello(a.cfg.Version, dv)
	h.Region = a.cfg.Region
	h.NomadVersion = a.nomad.version(ctx)
	return h
}

func (a *Agent) loadOrEnroll(ctx context.Context) error {
	if b, err := os.ReadFile(a.cfg.StateFile); err == nil {
		if err := json.Unmarshal(b, &a.state); err != nil {
			return fmt.Errorf("corrupt state file %s: %w", a.cfg.StateFile, err)
		}
		if len(a.state.SigningKey) != ed25519.PublicKeySize {
			return fmt.Errorf("state file %s predates signed commands: delete it and re-enroll", a.cfg.StateFile)
		}
		return nil
	}
	if a.cfg.Token == "" {
		return errors.New("not enrolled: set ORBIT_TOKEN to an enrollment token")
	}
	body, _ := json.Marshal(protocol.EnrollRequest{Token: a.cfg.Token, Host: a.hello(ctx)})
	resp, err := http.Post(a.cfg.URL+"/agent/v1/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("enroll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("enroll rejected (%d): %s", resp.StatusCode, e.Error)
	}
	var er protocol.EnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return err
	}
	if len(er.SigningKey) != ed25519.PublicKeySize {
		return errors.New("enroll: control plane did not provide a command signing key")
	}
	a.state = state{ServerID: er.ServerID, Secret: er.Secret, SigningKey: er.SigningKey}
	b, _ := json.Marshal(a.state)
	if err := os.MkdirAll(filepath.Dir(a.cfg.StateFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(a.cfg.StateFile, b, 0o600); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	a.log.Info("enrolled", "server_id", er.ServerID)
	return nil
}

var errUnauthorized = errors.New("unauthorized")

func (a *Agent) session(ctx context.Context) error {
	wsURL := "ws" + strings.TrimPrefix(a.cfg.URL, "http") + "/agent/v1/connect"
	c, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + a.state.ServerID + ":" + a.state.Secret}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errUnauthorized
		}
		return err
	}
	c.SetReadLimit(4 << 20)
	defer c.CloseNow()
	a.log.Info("connected", "url", a.cfg.URL)

	if err := a.send(ctx, c, protocol.TypeHello, a.hello(ctx)); err != nil {
		return err
	}
	a.mu.Lock()
	a.conn = c
	backlog := a.backlog
	a.backlog = nil
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.conn = nil; a.mu.Unlock() }()
	if len(backlog) > 0 {
		a.send(ctx, c, protocol.TypeMetrics, protocol.MetricsBatch{Samples: backlog})
	}

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var env protocol.Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Type {
		case protocol.TypeConfig:
			var cfg protocol.AgentConfig
			if json.Unmarshal(env.Payload, &cfg) == nil {
				a.dbs.configure(cfg.Databases)
			}
		case protocol.TypeCommand:
			var cmd protocol.Command
			if json.Unmarshal(env.Payload, &cmd) == nil {
				go a.handle(ctx, c, cmd)
			}
		}
	}
}

func (a *Agent) send(ctx context.Context, c *websocket.Conn, typ string, payload any) error {
	b, err := protocol.Encode(typ, payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b) // coder/websocket serializes concurrent writes
}

// sendNow sends on the current connection, dropping the frame when disconnected.
func (a *Agent) sendNow(ctx context.Context, typ string, payload any) bool {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	return c != nil && a.send(ctx, c, typ, payload) == nil
}
