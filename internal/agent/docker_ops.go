// Docker operations: container actions, logs, pulls, builds and one-shot helper containers.
package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"orbit/internal/protocol"
	"path"
	"sort"
	"strings"
	"time"
)

func (d *Docker) Action(ctx context.Context, action, id string) error {
	id = url.PathEscape(id)
	switch action {
	case protocol.ActionContainerStart:
		return d.do(ctx, "POST", "/containers/"+id+"/start", nil, nil)
	case protocol.ActionContainerStop:
		return d.do(ctx, "POST", "/containers/"+id+"/stop?t=10", nil, nil)
	case protocol.ActionContainerRestart:
		return d.do(ctx, "POST", "/containers/"+id+"/restart?t=10", nil, nil)
	case protocol.ActionContainerRemove:
		return d.do(ctx, "DELETE", "/containers/"+id+"?force=1", nil, nil)
	}
	return fmt.Errorf("unknown action %q", action)
}

func (d *Docker) Logs(ctx context.Context, id string, tail int) (string, error) {
	if tail <= 0 || tail > 5000 {
		tail = 200
	}
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://docker/v1.43/containers/%s/logs?stdout=1&stderr=1&timestamps=1&tail=%d", url.PathEscape(id), tail), nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("docker logs: %d", resp.StatusCode)
	}
	return demux(raw), nil
}

// demux strips the 8-byte stream headers Docker adds to non-TTY container logs.
func demux(b []byte) string {
	var out bytes.Buffer
	for len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if 8+n > len(b) {
			n = len(b) - 8
		}
		out.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	out.Write(b) // TTY containers have no headers
	return out.String()
}

// Pull streams the pull and fails on the first error message Docker reports inline.
// shortcut: public registries only; add X-Registry-Auth when registry credentials land.
func (d *Docker) Pull(ctx context.Context, image string) error {
	ref, tag := image, "latest"
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		ref, tag = image[:i], image[i+1:]
	}
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"http://docker/v1.43/images/create?fromImage="+url.QueryEscape(ref)+"&tag="+url.QueryEscape(tag), nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct{ Error, Message string }
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if m.Error != "" || m.Message != "" {
			return fmt.Errorf("pull %s: %s", image, m.Error+m.Message)
		}
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("pull %s: status %d", image, resp.StatusCode)
	}
	return nil
}

func (d *Docker) CreateRaw(ctx context.Context, name string, body any) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := d.do(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), body, &out)
	return out.ID, err
}

func (d *Docker) Inspect(ctx context.Context, id string, out any) error {
	return d.do(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, out)
}

// Exists reports whether a container with this name or ID exists.
func (d *Docker) Exists(ctx context.Context, id string) bool {
	return d.Inspect(ctx, id, &struct{}{}) == nil
}

func (d *Docker) RemoveVolume(ctx context.Context, name string) error {
	err := d.do(ctx, "DELETE", "/volumes/"+url.PathEscape(name)+"?force=1", nil, nil)
	if err != nil && strings.Contains(err.Error(), ": 404") {
		return nil
	}
	return err
}

func sortedDirs(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	sort.Strings(out) // parents before children
	return out
}

// Upload copies files into a (created, not necessarily started) container.
func (d *Docker) Upload(ctx context.Context, id, dir string, files map[string]string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	for name := range files {
		// parent directories need their own entries or the extraction 404s
		for d := path.Dir(name); d != "." && d != "/" && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for _, d := range sortedDirs(dirs) {
		tw.WriteHeader(&tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: time.Now()})
	}
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), ModTime: time.Now()})
		tw.Write([]byte(content))
	}
	tw.Close()
	req, _ := http.NewRequestWithContext(ctx, "PUT", "http://docker/v1.43/containers/"+url.PathEscape(id)+"/archive?path="+url.QueryEscape(dir), &buf)
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("upload to container: status %d", resp.StatusCode)
	}
	return nil
}

func (d *Docker) Wait(ctx context.Context, id string) (int, error) {
	var out struct{ StatusCode int }
	err := d.do(ctx, "POST", "/containers/"+url.PathEscape(id)+"/wait", nil, &out)
	return out.StatusCode, err
}

// EnsureImage pulls the image unless it is already present locally.
func (d *Docker) EnsureImage(ctx context.Context, image string) error {
	if d.do(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, nil) == nil {
		return nil
	}
	return d.Pull(ctx, image)
}

// Run executes a one-shot helper container and returns its exit code and output.
func (d *Docker) Run(ctx context.Context, name string, body map[string]any) (int, string, error) {
	img, _ := body["Image"].(string)
	if err := d.EnsureImage(ctx, img); err != nil {
		return -1, "", err
	}
	d.Action(ctx, protocol.ActionContainerRemove, name) // leftover from an interrupted run
	id, err := d.CreateRaw(ctx, name, body)
	if err != nil {
		return -1, "", err
	}
	defer d.Action(context.WithoutCancel(ctx), protocol.ActionContainerRemove, id)
	if err := d.Action(ctx, protocol.ActionContainerStart, id); err != nil {
		return -1, "", err
	}
	code, err := d.Wait(ctx, id)
	if err != nil {
		return -1, "", err
	}
	out, _ := d.rawLogs(context.WithoutCancel(ctx), id)
	return code, out, nil
}

func (d *Docker) rawLogs(ctx context.Context, id string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/v1.43/containers/"+url.PathEscape(id)+"/logs?stdout=1&stderr=1&tail=200", nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return demux(raw), nil
}

// Build runs a remote-context (git) build and streams its output.
func (d *Docker) Build(ctx context.Context, b protocol.BuildArgs, tag string, line func(string)) error {
	remote := b.Remote
	if b.Ref != "" || b.Context != "" {
		remote += "#" + b.Ref
		if b.Context != "" {
			remote += ":" + strings.Trim(b.Context, "/")
		}
	}
	q := url.Values{"remote": {remote}, "t": {tag}, "rm": {"1"}, "forcerm": {"1"}}
	if b.Dockerfile != "" {
		q.Set("dockerfile", b.Dockerfile)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://docker/v1.43/build?"+q.Encode(), nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Stream, Status, Error, Message string
		}
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if m.Error != "" || m.Message != "" {
			return fmt.Errorf("build: %s", m.Error+m.Message)
		}
		if s := strings.TrimRight(m.Stream+m.Status, "\n"); s != "" {
			line(s)
		}
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("build: status %d", resp.StatusCode)
	}
	return nil
}
