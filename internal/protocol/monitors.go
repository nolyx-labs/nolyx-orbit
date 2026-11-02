// Uptime checks run by agents as extra check locations, besides the control plane.
package protocol

import "time"

const TypeMonitorResults = "monitor_results" // agent → cp: MonitorResults

type MonitorConfig struct {
	ID              string `json:"id"`
	Type            string `json:"type"` // http | tcp
	Target          string `json:"target"`
	ExpectedStatus  string `json:"expected_status"`
	Keyword         string `json:"keyword,omitempty"`
	TimeoutMS       int    `json:"timeout_ms"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type MonitorResult struct {
	MonitorID  string     `json:"monitor_id,omitempty"`
	TS         time.Time  `json:"ts"`
	OK         bool       `json:"ok"`
	LatencyMS  int        `json:"latency_ms"`
	StatusCode int        `json:"status_code,omitempty"`
	Error      string     `json:"error,omitempty"`
	CertExpiry *time.Time `json:"cert_expires_at,omitempty"`
}

type MonitorResults struct {
	Results []MonitorResult `json:"results"`
}
