// Uptime checks this server runs as an extra location, for monitors that list it.
package agent

import (
	"context"
	"sync"
	"time"

	"orbit/internal/protocol"
	"orbit/internal/uptime"
)

func (a *Agent) setMonitors(ms []protocol.MonitorConfig) {
	a.mu.Lock()
	a.monitors = ms
	a.mu.Unlock()
}

// runMonitors checks each monitor on its interval and sends the results. Checks refuse private addresses: they
// stand in for visitors on the internet, not for reaching into the networks this server can see.
func (a *Agent) runMonitors(ctx context.Context) {
	next := map[string]time.Time{}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		ms, connected := a.monitors, a.conn != nil
		a.mu.Unlock()
		if !connected {
			continue
		}
		now, due, kept := time.Now(), []protocol.MonitorConfig{}, map[string]time.Time{}
		for _, m := range ms {
			kept[m.ID] = next[m.ID]
			if now.Before(next[m.ID]) {
				continue
			}
			kept[m.ID] = now.Add(time.Duration(max(m.IntervalSeconds, 30)) * time.Second)
			due = append(due, m)
		}
		next = kept
		if len(due) == 0 {
			continue
		}
		results := make([]protocol.MonitorResult, len(due))
		var wg sync.WaitGroup
		for i, m := range due {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = uptime.Check(ctx, m, uptime.PublicDialer())
			}()
		}
		wg.Wait()
		a.sendNow(ctx, protocol.TypeMonitorResults, protocol.MonitorResults{Results: results})
	}
}
