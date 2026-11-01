package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"orbit/internal/protocol"
)

// nomadClient talks to the local Nomad agent's HTTP API. Nomad is optional: when it isn't
// reachable, nothing is reported and Nomad commands fail with a clear error.
type nomadClient struct {
	addr, token string
	http        *http.Client
}

func newNomadClient(addr, token string) *nomadClient {
	if addr == "" {
		addr = "http://127.0.0.1:4646"
	}
	return &nomadClient{addr: strings.TrimSuffix(addr, "/"), token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

func (n *nomadClient) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, n.addr+path, r)
	if err != nil {
		return err
	}
	if n.token != "" {
		req.Header.Set("X-Nomad-Token", n.token)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("nomad %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type nomadSelf struct {
	Config struct {
		Region     string
		Datacenter string
		Version    struct{ Version string }
	} `json:"config"`
}

func (n *nomadClient) version(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var s nomadSelf
	if n.do(ctx, "GET", "/v1/agent/self", nil, &s) != nil {
		return ""
	}
	return s.Config.Version.Version
}

func (a *Agent) reportNomad(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if snap, err := a.nomad.snapshot(ctx); err == nil {
			a.sendNow(ctx, protocol.TypeNomad, snap)
		}
	}
}

func (n *nomadClient) snapshot(ctx context.Context) (protocol.NomadSnapshot, error) {
	snap := protocol.NomadSnapshot{TS: time.Now().UTC()}
	var self nomadSelf
	if err := n.do(ctx, "GET", "/v1/agent/self", nil, &self); err != nil {
		return snap, err
	}
	snap.Region, snap.Datacenter = self.Config.Region, self.Config.Datacenter
	n.do(ctx, "GET", "/v1/status/leader", nil, &snap.Leader)
	n.do(ctx, "GET", "/v1/status/peers", nil, &snap.Peers)
	for path, dst := range map[string]*json.RawMessage{
		"/v1/nodes?resources=true": &snap.Nodes, "/v1/jobs": &snap.Jobs,
		"/v1/allocations": &snap.Allocations, "/v1/deployments": &snap.Deployments,
	} {
		if err := n.do(ctx, "GET", path, nil, dst); err != nil {
			return snap, err
		}
	}
	return snap, nil
}

// register submits a job and follows its deployment until healthy or failed.
func (n *nomadClient) register(ctx context.Context, args protocol.NomadJobArgs, progress func(protocol.Progress)) error {
	step := stepper(progress)
	log := func(format string, v ...any) { progress(protocol.Progress{Line: fmt.Sprintf(format, v...)}) }
	var reg struct {
		EvalID         string
		JobModifyIndex uint64
	}
	if err := step("Submitting job", func() error {
		return n.do(ctx, "POST", "/v1/jobs", map[string]json.RawMessage{"Job": args.Job}, &reg)
	}); err != nil {
		return err
	}
	log("job %s registered (eval %s)", args.JobID, reg.EvalID)
	return step("Rolling out", func() error {
		deadline := time.Now().Add(15 * time.Minute)
		last := ""
		for time.Now().Before(deadline) {
			time.Sleep(3 * time.Second)
			var d *struct {
				ID                string
				JobVersion        uint64
				Status            string
				StatusDescription string
				TaskGroups        map[string]struct{ DesiredTotal, PlacedAllocs, HealthyAllocs, UnhealthyAllocs int }
			}
			if err := n.do(ctx, "GET", "/v1/job/"+url.PathEscape(args.JobID)+"/deployment", nil, &d); err != nil {
				return err
			}
			if reason := n.placementFailure(ctx, args.JobID); reason != "" {
				return fmt.Errorf("Nomad could not place the job: %s", reason)
			}
			if d == nil {
				continue
			}
			var parts []string
			for tg, s := range d.TaskGroups {
				parts = append(parts, fmt.Sprintf("%s %d/%d healthy", tg, s.HealthyAllocs, s.DesiredTotal))
			}
			if msg := d.Status + ": " + strings.Join(parts, ", "); msg != last {
				log("%s", msg)
				last = msg
			}
			switch d.Status {
			case "successful":
				return nil
			case "failed", "cancelled":
				return fmt.Errorf("deployment %s: %s", d.Status, d.StatusDescription)
			}
		}
		return fmt.Errorf("deployment did not finish within 15 minutes")
	})
}

// placementFailure explains a blocked evaluation (e.g. no node has enough CPU), so a rollout fails fast
// instead of waiting for the deadline.
func (n *nomadClient) placementFailure(ctx context.Context, jobID string) string {
	var evals []struct {
		Status         string
		CreateIndex    uint64
		FailedTGAllocs map[string]struct {
			NodesEvaluated     int
			NodesExhausted     int
			DimensionExhausted map[string]int
			ConstraintFiltered map[string]int
		}
	}
	if n.do(ctx, "GET", "/v1/job/"+url.PathEscape(jobID)+"/evaluations", nil, &evals) != nil || len(evals) == 0 {
		return ""
	}
	latest := evals[0]
	for _, e := range evals {
		if e.CreateIndex > latest.CreateIndex {
			latest = e
		}
	}
	if latest.Status != "blocked" {
		return ""
	}
	var reasons []string
	for tg, f := range latest.FailedTGAllocs {
		for dim, nodes := range f.DimensionExhausted {
			reasons = append(reasons, fmt.Sprintf("%s: %s exhausted on %d node(s)", tg, dim, nodes))
		}
		for c, nodes := range f.ConstraintFiltered {
			reasons = append(reasons, fmt.Sprintf("%s: %d node(s) filtered by %s", tg, nodes, c))
		}
		if len(reasons) == 0 {
			reasons = append(reasons, fmt.Sprintf("%s: no eligible node among %d evaluated", tg, f.NodesEvaluated))
		}
	}
	return strings.Join(reasons, "; ")
}

func (n *nomadClient) stop(ctx context.Context, jobID string) error {
	return n.do(ctx, "DELETE", "/v1/job/"+url.PathEscape(jobID), nil, nil)
}
