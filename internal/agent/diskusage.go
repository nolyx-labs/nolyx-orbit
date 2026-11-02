// "What's using the disk": du over one filesystem and Docker's disk usage, plus Docker cleanup.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"orbit/internal/protocol"
)

const maxDirs = 40

func (a *Agent) diskUsage(ctx context.Context, args protocol.DiskArgs) (protocol.DiskUsage, error) {
	out := protocol.DiskUsage{Path: args.Path, Dirs: []protocol.DirUsage{}}
	mounted := false
	for _, d := range parseMounts(a.host.read("/proc/1/mounts")) {
		mounted = mounted || d.Mount == args.Path
	}
	if !mounted || filepath.Clean(args.Path) != args.Path {
		return out, fmt.Errorf("%q is not one of this server's filesystems", args.Path)
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	df, err := a.docker.diskUsage(dctx)
	cancel()
	if err == nil {
		out.Docker = &df
	}
	if a.cfg.HostRoot != "" {
		out.Note = "Folder sizes are " + errContainerMode.Error() // Docker usage still works over the socket
		return out, nil
	}
	dctx, cancel = context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	// du exits non-zero on unreadable paths (e.g. /proc entries) while still reporting the rest, so its error is ignored.
	raw, _ := run(dctx, "du", "-x", "-k", "-d", "2", "--", args.Path)
	out.Partial = errors.Is(dctx.Err(), context.DeadlineExceeded)
	out.Dirs = parseDU(raw)
	return out, nil
}

// parseDU reads `du -k` lines ("<KiB>\t<path>") and keeps the largest entries.
func parseDU(out string) []protocol.DirUsage {
	dirs := []protocol.DirUsage{}
	for _, line := range strings.Split(out, "\n") {
		size, path, ok := strings.Cut(line, "\t")
		n, err := strconv.ParseUint(strings.TrimSpace(size), 10, 64)
		if ok && err == nil {
			dirs = append(dirs, protocol.DirUsage{Path: path, Size: n * 1024})
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return dirs[i].Size > dirs[j].Size })
	return dirs[:min(len(dirs), maxDirs)]
}

type dockerDF struct {
	LayersSize int64
	Images     []struct {
		Size, SharedSize int64
		Containers       int64
	}
	Containers []struct {
		SizeRw int64
		State  string
	}
	Volumes []struct {
		UsageData *struct{ Size, RefCount int64 }
	}
	BuildCache []struct {
		Size          int64
		InUse, Shared bool
	}
}

func (d *Docker) diskUsage(ctx context.Context) (protocol.DockerDF, error) {
	var raw dockerDF
	if err := d.do(ctx, "GET", "/system/df", nil, &raw); err != nil {
		return protocol.DockerDF{}, err
	}
	return summarizeDF(raw), nil
}

// summarizeDF mirrors `docker system df`. shortcut: image reclaimable counts whole images without containers,
// ignoring layers they share with used images, so it can overstate slightly.
func summarizeDF(raw dockerDF) protocol.DockerDF {
	pos := func(n int64) uint64 { return uint64(max(n, 0)) }
	var df protocol.DockerDF
	df.Images = protocol.DFLine{Count: len(raw.Images), Size: pos(raw.LayersSize)}
	for _, im := range raw.Images {
		if im.Containers == 0 {
			df.Images.Reclaimable += pos(im.Size)
		}
	}
	df.Images.Reclaimable = min(df.Images.Reclaimable, df.Images.Size)
	df.Containers.Count = len(raw.Containers)
	for _, c := range raw.Containers {
		df.Containers.Size += pos(c.SizeRw)
		if c.State != "running" {
			df.Containers.Reclaimable += pos(c.SizeRw)
		}
	}
	df.Volumes.Count = len(raw.Volumes)
	for _, v := range raw.Volumes {
		if v.UsageData != nil {
			df.Volumes.Size += pos(v.UsageData.Size)
			if v.UsageData.RefCount == 0 {
				df.Volumes.Reclaimable += pos(v.UsageData.Size)
			}
		}
	}
	df.BuildCache.Count = len(raw.BuildCache)
	for _, b := range raw.BuildCache {
		df.BuildCache.Size += pos(b.Size)
		if !b.InUse && !b.Shared {
			df.BuildCache.Reclaimable += pos(b.Size)
		}
	}
	return df
}

// prune removes images no container uses and the whole build cache. Volumes and stopped containers are left alone.
func (d *Docker) prune(ctx context.Context) (protocol.DockerPruneResult, error) {
	var res protocol.DockerPruneResult
	var out struct{ SpaceReclaimed uint64 }
	filter, _ := json.Marshal(map[string][]string{"dangling": {"false"}})
	if err := d.do(ctx, "POST", "/images/prune?filters="+url.QueryEscape(string(filter)), nil, &out); err != nil {
		return res, err
	}
	res.Reclaimed = out.SpaceReclaimed
	out.SpaceReclaimed = 0
	if err := d.do(ctx, "POST", "/build/prune?all=1", nil, &out); err != nil {
		return res, err
	}
	res.Reclaimed += out.SpaceReclaimed
	return res, nil
}
