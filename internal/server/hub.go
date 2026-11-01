// Hub: connected agents, in-flight commands and SSE subscribers.
package server

import (
	"encoding/json"
	"orbit/internal/protocol"
	"sync"

	"github.com/coder/websocket"
)

// Hub tracks connected agents, in-flight commands and SSE subscribers. In-process only (single instance).
type Hub struct {
	mu      sync.Mutex
	agents  map[string]*agentConn               // server id
	pending map[string]chan protocol.Result     // command id
	subs    map[string]map[chan string]struct{} // project id
}

type agentConn struct {
	ws        *websocket.Conn
	serverID  string
	projectID string
}

func newHub() *Hub {
	return &Hub{agents: map[string]*agentConn{}, pending: map[string]chan protocol.Result{}, subs: map[string]map[chan string]struct{}{}}
}

func (h *Hub) publish(projectID, topic, id string) {
	msg, _ := json.Marshal(map[string]string{"topic": topic, "id": id})
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[projectID] {
		select {
		case ch <- string(msg):
		default: // slow client; it refetches on the next notification anyway
		}
	}
}

func (h *Hub) subscribe(projectID string) (chan string, func()) {
	ch := make(chan string, 32)
	h.mu.Lock()
	if h.subs[projectID] == nil {
		h.subs[projectID] = map[chan string]struct{}{}
	}
	h.subs[projectID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[projectID], ch)
		h.mu.Unlock()
	}
}

func (h *Hub) connected(serverID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agents[serverID] != nil
}
