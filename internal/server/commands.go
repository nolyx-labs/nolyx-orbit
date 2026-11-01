// Signed commands and frames sent to agents.
package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"orbit/internal/protocol"
	"time"

	"github.com/coder/websocket"
)

var errOffline = errStatus(409, "server is offline")

// sendCommand records the command in the ledger and delivers it to the agent. With wait, it blocks
// for the result. Callers must have already done authorization and auditing.
func (s *Server) sendCommand(ctx context.Context, id, serverID, userID, action string, args any, wait bool) (protocol.Result, error) {
	raw, _ := json.Marshal(args)
	s.hub.mu.Lock()
	ac := s.hub.agents[serverID]
	var ch chan protocol.Result
	if ac != nil && wait {
		ch = make(chan protocol.Result, 1)
		s.hub.pending[id] = ch
	}
	s.hub.mu.Unlock()
	if ac == nil {
		return protocol.Result{}, errOffline
	}
	defer func() { s.hub.mu.Lock(); delete(s.hub.pending, id); s.hub.mu.Unlock() }()

	if _, err := s.db.Exec(ctx, `insert into commands (id, server_id, action, args, requested_by) values ($1,$2,$3,$4,nullif($5,'')::uuid)`,
		id, serverID, action, raw, userID); err != nil {
		return protocol.Result{}, err
	}
	cmd := protocol.Command{ID: id, Action: action, Args: raw, ExpiresAt: time.Now().Add(time.Minute).UTC()}
	cmd.Signature = ed25519.Sign(s.signer, protocol.SigningPayload(serverID, cmd))
	frame, _ := protocol.Encode(protocol.TypeCommand, cmd)
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := ac.ws.Write(wctx, websocket.MessageText, frame)
	cancel()
	if err != nil {
		s.db.Exec(context.WithoutCancel(ctx), `update commands set status='failed', error=$2, completed_at=now() where id=$1`, id, "delivery failed: "+err.Error())
		return protocol.Result{}, errStatus(502, "could not deliver command to agent")
	}
	if !wait {
		return protocol.Result{}, nil
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(90 * time.Second):
		return protocol.Result{}, errStatus(504, "agent did not respond in time")
	case <-ctx.Done():
		return protocol.Result{}, ctx.Err()
	}
}

// sendFrame delivers a non-command frame to a connected agent.
func (s *Server) sendFrame(ctx context.Context, serverID, typ string, payload any) error {
	s.hub.mu.Lock()
	ac := s.hub.agents[serverID]
	s.hub.mu.Unlock()
	if ac == nil {
		return nil // it gets the current config when it reconnects
	}
	b, err := protocol.Encode(typ, payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ac.ws.Write(ctx, websocket.MessageText, b)
}
