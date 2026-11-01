package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"orbit/internal/protocol"
)

// Docker talks to the Docker Engine API over the unix socket; no SDK needed.
type Docker struct {
	http *http.Client
}

func NewDocker(socket string) *Docker {
	return &Docker{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

func (d *Docker) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.43"+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		var e struct{ Message string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("docker %s %s: %d %s", method, path, resp.StatusCode, e.Message)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func (d *Docker) Version(ctx context.Context) (string, error) {
	var v struct{ Version string }
	err := d.do(ctx, "GET", "/version", nil, &v)
	return v.Version, err
}

type dockerContainer struct {
	ID      string `json:"Id"`
	Names   []string
	Image   string
	State   string
	Status  string
	Created int64
	Labels  map[string]string
	Ports   []struct {
		IP          string
		PrivatePort int
		PublicPort  int
		Type        string
	}
	NetworkSettings struct {
		Networks map[string]struct{ IPAddress string }
	}
}

// ip returns the container's address on its (first) Docker network.
func (c dockerContainer) ip() string {
	for _, n := range c.NetworkSettings.Networks {
		if n.IPAddress != "" {
			return n.IPAddress
		}
	}
	return ""
}

func (d *Docker) list(ctx context.Context, filter string) ([]dockerContainer, error) {
	path := "/containers/json?all=1"
	if filter != "" {
		path += "&filters=" + url.QueryEscape(filter)
	}
	var cs []dockerContainer
	err := d.do(ctx, "GET", path, nil, &cs)
	return cs, err
}

// Snapshot lists all containers with live stats. Stats calls run concurrently (each takes ~1s).
func (d *Docker) Snapshot(ctx context.Context) ([]protocol.Container, error) {
	cs, err := d.list(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Container, len(cs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, c := range cs {
		pc := protocol.Container{
			ID: c.ID, Image: c.Image, State: c.State, Status: c.Status,
			Created: time.Unix(c.Created, 0).UTC(), Labels: c.Labels, Ports: []string{},
		}
		if len(c.Names) > 0 {
			pc.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, p := range c.Ports {
			if p.PublicPort != 0 && !strings.Contains(p.IP, ":") {
				pc.Ports = append(pc.Ports, fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type))
			}
		}
		out[i] = pc
		wg.Add(1)
		go func(pc *protocol.Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d.enrich(ctx, pc)
		}(&out[i])
	}
	wg.Wait()
	return out, nil
}

func (d *Docker) enrich(ctx context.Context, c *protocol.Container) {
	var insp struct {
		RestartCount int
		State        struct {
			Health *struct{ Status string }
		}
	}
	if d.do(ctx, "GET", "/containers/"+c.ID+"/json", nil, &insp) == nil {
		c.RestartCount = insp.RestartCount
		if insp.State.Health != nil {
			c.Health = insp.State.Health.Status
		}
	}
	if c.State != "running" {
		return
	}
	var s dockerStats
	if d.do(ctx, "GET", "/containers/"+c.ID+"/stats?stream=false", nil, &s) == nil {
		c.CPU, c.MemUsed, c.MemLimit = s.compute()
	}
}

type dockerStats struct {
	CPUStats    cpuStats `json:"cpu_stats"`
	PreCPUStats cpuStats `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  uint64 `json:"online_cpus"`
}

// compute mirrors `docker stats`: CPU as percent of one core (can exceed 100), memory minus page cache.
func (s dockerStats) compute() (cpu float64, memUsed, memLimit uint64) {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		cpu = cpuDelta / sysDelta * float64(max(s.CPUStats.OnlineCPUs, 1)) * 100
	}
	memUsed = s.MemoryStats.Usage
	if c := s.MemoryStats.Stats["inactive_file"]; c < memUsed { // cgroup v2
		memUsed -= c
	}
	return cpu, memUsed, s.MemoryStats.Limit
}
