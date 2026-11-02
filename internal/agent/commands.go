// Command intake: allowlist, signature, expiry and replay checks, then dispatch.
package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"orbit/internal/protocol"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// accept enforces the allowlist, signature, expiry and replay checks before anything runs.
func (a *Agent) accept(cmd protocol.Command, now time.Time) error {
	if !protocol.Actions[cmd.Action] {
		return fmt.Errorf("action %q not allowed", cmd.Action)
	}
	if !ed25519.Verify(a.state.SigningKey, protocol.SigningPayload(a.state.ServerID, cmd), cmd.Signature) {
		return errors.New("invalid command signature")
	}
	if cmd.ID == "" || now.After(cmd.ExpiresAt) {
		return errors.New("command expired")
	}
	if !a.seen.add(cmd.ID) {
		return errors.New("duplicate command id")
	}
	return nil
}

func (a *Agent) handle(ctx context.Context, c *websocket.Conn, cmd protocol.Command) {
	res := protocol.Result{ID: cmd.ID}
	out, err := a.exec(ctx, c, cmd)
	if err != nil {
		res.Error = err.Error()
	} else {
		res.OK = true
		res.Output, _ = json.Marshal(out)
	}
	a.log.Info("command", "id", cmd.ID, "action", cmd.Action, "ok", res.OK, "err", res.Error)
	a.send(ctx, c, protocol.TypeResult, res)
}

func (a *Agent) exec(ctx context.Context, c *websocket.Conn, cmd protocol.Command) (any, error) {
	if err := a.accept(cmd, time.Now()); err != nil {
		return nil, err
	}
	progress := func(p protocol.Progress) { p.ID = cmd.ID; a.send(ctx, c, protocol.TypeProgress, p) }
	long, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	switch cmd.Action {
	case protocol.ActionDeploy:
		var args protocol.DeployArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return nil, a.deploy(long, cmd.ID, args, progress)
	case protocol.ActionDBProvision, protocol.ActionDBReplicaCreate, protocol.ActionDBResync, protocol.ActionDBRemove:
		var args protocol.PGArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return a.pgManage(long, cmd.Action, args, progress)
	case protocol.ActionDBPromote:
		var args protocol.DBArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return nil, a.dbs.promote(long, args.DatabaseID)
	case protocol.ActionDBBackup, protocol.ActionDBRestore:
		var args protocol.BackupArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		if cmd.Action == protocol.ActionDBBackup {
			return a.pgBackup(long, args, progress)
		}
		return nil, a.pgRestore(long, args, progress)
	case protocol.ActionNomadRegister, protocol.ActionNomadStop:
		var args protocol.NomadJobArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		if cmd.Action == protocol.ActionNomadStop {
			return nil, a.nomad.stop(long, args.JobID)
		}
		return nil, a.nomad.register(long, args, progress)
	case protocol.ActionNomadInstall:
		var args protocol.NomadInstallArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return nil, a.nomadInstall(long, args, progress)
	case protocol.ActionOSCheck, protocol.ActionOSInstall, protocol.ActionOSHold, protocol.ActionOSReboot:
		return a.osExec(long, cmd.Action, cmd.Args, progress)
	}
	var args protocol.ContainerArgs
	if err := json.Unmarshal(cmd.Args, &args); err != nil || args.ContainerID == "" {
		return nil, errors.New("container_id required")
	}
	cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
	defer ccancel()
	if cmd.Action == protocol.ActionContainerExec {
		return a.docker.Exec(cctx, args.ContainerID, args.Cmd, time.Duration(min(max(args.Timeout, 1), 55))*time.Second)
	}
	if cmd.Action == protocol.ActionContainerLogs {
		return a.docker.Logs(cctx, args.ContainerID, args.Tail)
	}
	return nil, a.docker.Action(cctx, cmd.Action, args.ContainerID)
}

// seenSet is a bounded FIFO set of command IDs for replay protection.
type seenSet struct {
	mu    sync.Mutex
	ids   map[string]struct{}
	order []string
	cap   int
}

func newSeenSet(n int) *seenSet { return &seenSet{ids: map[string]struct{}{}, cap: n} }

func (s *seenSet) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false
	}
	s.ids[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.cap {
		delete(s.ids, s.order[0])
		s.order = s.order[1:]
	}
	return true
}
