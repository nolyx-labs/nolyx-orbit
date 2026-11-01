// Periodic host metrics and container snapshots.
package agent

import (
	"context"
	"orbit/internal/protocol"
	"time"
)

// collect samples metrics every 10s and containers every 15s regardless of connectivity.
func (a *Agent) collect(ctx context.Context) {
	a.host.Sample() // prime rate counters
	mt, ct := time.NewTicker(10*time.Second), time.NewTicker(15*time.Second)
	defer mt.Stop()
	defer ct.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-mt.C:
			m := a.host.Sample()
			a.mu.Lock()
			c := a.conn
			if c == nil {
				a.backlog = append(a.backlog, m)
				if len(a.backlog) > maxBacklog {
					a.backlog = a.backlog[len(a.backlog)-maxBacklog:]
				}
			}
			a.mu.Unlock()
			if c != nil {
				a.send(ctx, c, protocol.TypeMetrics, protocol.MetricsBatch{Samples: []protocol.Metrics{m}})
			}
		case <-ct.C:
			sctx, cancel := context.WithTimeout(ctx, 12*time.Second)
			cs, err := a.docker.Snapshot(sctx)
			cancel()
			if err != nil {
				a.log.Debug("docker snapshot failed", "err", err)
				continue // Docker absent or down: keep reporting host metrics
			}
			a.sendNow(ctx, protocol.TypeContainers, protocol.ContainerSnapshot{TS: time.Now().UTC(), Containers: cs})
		}
	}
}
