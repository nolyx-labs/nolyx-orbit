// Shipped container logs and proxy request metrics.
package protocol

import "time"

type LogLine struct {
	ContainerID string    `json:"container_id"`
	Container   string    `json:"container"`
	App         string    `json:"app,omitempty"`
	TS          time.Time `json:"ts"`
	Stream      string    `json:"stream"`
	Line        string    `json:"line"`
	Unit        string    `json:"unit,omitempty"`  // systemd service, for journal lines (no container)
	Level       string    `json:"level,omitempty"` // from the journal's priority; container lines are guessed by the control plane
}

type LogBatch struct {
	Lines   []LogLine `json:"lines"`
	Dropped int       `json:"dropped"`
}

type AppMetric struct {
	App      string  `json:"app"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"` // 5xx and upstream failures
	AvgMs    float64 `json:"avg_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

type AppMetricsBatch struct {
	TS   time.Time   `json:"ts"`
	Apps []AppMetric `json:"apps"`
}
