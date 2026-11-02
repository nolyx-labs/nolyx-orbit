// Server backups: every Orbit service's volumes and bind mounts (or pg_dump for managed PostgreSQL),
// gzipped and encrypted on this server, then stored in S3 or on another Orbit server.
package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"orbit/internal/backup"
	"orbit/internal/protocol"
)

// helperImage backs helper containers: created but never started to read volumes through Docker's
// archive API (which mounts them), and run to wipe volumes before a restore.
const helperImage = "busybox:1.36.1"

type mountInfo struct {
	Type        string
	Name        string
	Source      string
	Destination string
}

// containerInfo is the part of `docker inspect` that discovery needs.
type containerInfo struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string
	}
	State struct {
		Running, Paused bool
	}
	Mounts []mountInfo
}

type service struct {
	Key        string // app/<name> or db/<name>
	Containers []containerInfo
	Mounts     []mountInfo
}

var (
	// skipBind: host paths that are never service data (the Docker socket, kernel filesystems).
	skipBind     = regexp.MustCompile(`^/(proc|sys|dev|run|var/run)(/|$)|docker\.sock$`)
	serviceKeyRe = regexp.MustCompile(`^(app|db)/[a-z0-9][a-z0-9-]{0,62}$`)
	uuidRe       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	s3PrefixRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`)
	backupsVol   = mountInfo{Type: "volume", Name: "orbit-backups", Destination: "/backups"}
)

// discoverServices groups Orbit-labelled containers into services with their volumes and bind mounts.
// Managed replicas are skipped: they're re-cloned from their primary.
func discoverServices(cs []containerInfo) []service {
	byKey := map[string]*service{}
	for _, c := range cs {
		l := c.Config.Labels
		key := ""
		if l["orbit.app"] != "" {
			key = "app/" + l["orbit.app"]
		} else if l["orbit.db"] != "" && l["orbit.db.role"] != "replica" {
			key = "db/" + l["orbit.db"]
		}
		if !serviceKeyRe.MatchString(key) {
			continue
		}
		s := byKey[key]
		if s == nil {
			s = &service{Key: key}
			byKey[key] = s
		}
		s.Containers = append(s.Containers, c)
		for _, m := range c.Mounts {
			if m.Type != "volume" && (m.Type != "bind" || skipBind.MatchString(m.Source)) {
				continue
			}
			if !slices.ContainsFunc(s.Mounts, func(x mountInfo) bool { return x.Destination == m.Destination }) {
				s.Mounts = append(s.Mounts, m)
			}
		}
	}
	out := make([]service, 0, len(byKey))
	for _, s := range byKey {
		sort.Slice(s.Mounts, func(i, j int) bool { return s.Mounts[i].Destination < s.Mounts[j].Destination })
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (a *Agent) services(ctx context.Context) ([]service, error) {
	list, err := a.docker.list(ctx, `{"label":["orbit.managed=true"]}`)
	if err != nil {
		return nil, err
	}
	cs := make([]containerInfo, 0, len(list))
	for _, c := range list {
		var ci containerInfo
		if err := a.docker.Inspect(ctx, c.ID, &ci); err != nil {
			return nil, err
		}
		cs = append(cs, ci)
	}
	return discoverServices(cs), nil
}

// busyServices allows one backup or restore per service at a time.
var busyServices sync.Map

func lockService(key string) (func(), error) {
	if _, busy := busyServices.LoadOrStore(key, true); busy {
		return nil, fmt.Errorf("%s is busy with another backup or restore", key)
	}
	return func() { busyServices.Delete(key) }, nil
}

func checkBackupArgs(args protocol.ServerBackupArgs) error {
	d := args.Dest
	switch {
	case len(args.Key) != 32 || !uuidRe.MatchString(args.Folder):
	case !slices.Contains([]string{protocol.BackupScheduled, protocol.BackupManual, protocol.BackupPreDeploy, protocol.BackupPreRestore}, args.Kind):
	case args.Only != "" && !serviceKeyRe.MatchString(args.Only):
	case (d.S3 == nil) == (d.Server == nil):
	case d.S3 != nil && (d.S3.Bucket == "" || !s3PrefixRe.MatchString(d.S3.Prefix)):
	case d.Server != nil && (d.Server.Addr == "" || d.Server.HostKey == ""):
	default:
		return nil
	}
	return errors.New("invalid backup arguments")
}

func managedDB(args protocol.ServerBackupArgs, key string) *protocol.ServiceDB {
	for i, d := range args.Databases {
		if "db/"+d.Name == key {
			return &args.Databases[i]
		}
	}
	return nil
}

// serverBackup archives every service with data (or only args.Only) and uploads each archive.
// One service failing doesn't stop the others.
func (a *Agent) serverBackup(ctx context.Context, args protocol.ServerBackupArgs, progress func(protocol.Progress)) (protocol.ServerBackupResult, error) {
	res := protocol.ServerBackupResult{Backups: []protocol.ServiceBackup{}}
	if err := checkBackupArgs(args); err != nil {
		return res, err
	}
	svcs, err := a.services(ctx)
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	step := stepper(progress)
	for _, s := range svcs {
		if (args.Only != "" && s.Key != args.Only) || (len(s.Mounts) == 0 && managedDB(args, s.Key) == nil) {
			continue
		}
		var b protocol.ServiceBackup
		step("Backing up "+s.Key, func() error {
			b = a.backupService(ctx, args, s)
			if b.Error != "" {
				return errors.New(b.Error)
			}
			return nil
		})
		res.Backups = append(res.Backups, b)
	}
	return res, nil
}

func (a *Agent) backupService(ctx context.Context, args protocol.ServerBackupArgs, s service) protocol.ServiceBackup {
	start := time.Now()
	typ, name, _ := strings.Cut(s.Key, "/")
	b := protocol.ServiceBackup{Service: s.Key, Kind: args.Kind,
		File: fmt.Sprintf("%s-%s-%s-%s.tar.gz.enc", typ, name, start.UTC().Format("20060102-150405"), args.Kind)}
	err := func() error {
		unlock, err := lockService(s.Key)
		if err != nil {
			return err
		}
		defer unlock()
		f, err := a.stageFile(b.File)
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if err := a.writeArchive(ctx, args, s, f); err != nil {
			return err
		}
		st, err := f.Stat()
		if err != nil {
			return err
		}
		b.Size = st.Size()
		b.Location, err = a.store(ctx, args.Dest, args.Folder, b.File, f, b.Size)
		return err
	}()
	if err != nil {
		b.Error = err.Error()
	}
	b.Millis = time.Since(start).Milliseconds()
	return b
}

// stageFile creates a private file next to the agent state; archives are written here first so
// services are paused only while their data is read locally, never during the upload.
func (a *Agent) stageFile(name string) (*os.File, error) {
	dir := filepath.Join(filepath.Dir(a.cfg.StateFile), "backup-staging")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, randHex()+"-"+name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
}

func randHex() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// writeArchive writes encrypt(gzip(tar)) of the service's data to w.
func (a *Agent) writeArchive(ctx context.Context, args protocol.ServerBackupArgs, s service, w io.Writer) error {
	enc, err := backup.Encrypt(w, args.Key)
	if err != nil {
		return err
	}
	gz, _ := gzip.NewWriterLevel(enc, gzip.BestSpeed)
	if db := managedDB(args, s.Key); db != nil {
		err = a.dumpDB(ctx, *db, gz)
	} else {
		err = a.archiveVolumes(ctx, s, gz)
	}
	if err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return enc.Close()
}

// archiveVolumes tars the service's mounts. Its running containers are paused (`docker pause`)
// while the data is read so the archive is consistent; they're unpaused even on error.
// Containers someone else paused stay paused.
func (a *Agent) archiveVolumes(ctx context.Context, s service, w io.Writer) error {
	id, err := a.helper(ctx, s.Mounts, true)
	if err != nil {
		return err
	}
	defer a.docker.Action(context.WithoutCancel(ctx), protocol.ActionContainerRemove, id)
	for _, c := range s.Containers {
		if !c.State.Running || c.State.Paused {
			continue
		}
		if err := a.docker.do(ctx, "POST", "/containers/"+c.ID+"/pause", nil, nil); err != nil {
			return err
		}
		defer func(cid string) {
			if err := a.docker.do(context.WithoutCancel(ctx), "POST", "/containers/"+cid+"/unpause", nil, nil); err != nil {
				a.log.Error("unpause after backup failed", "container", cid, "err", err)
			}
		}(c.ID)
	}
	return a.docker.copyFrom(ctx, id, "/orbit", w)
}

// dumpDB runs the existing pg_dump path into the orbit-backups volume and archives the dump.
func (a *Agent) dumpDB(ctx context.Context, db protocol.ServiceDB, w io.Writer) error {
	file := "orbit-snap-" + randHex() + ".dump"
	if _, err := a.pgBackup(ctx, protocol.BackupArgs{DB: db.DB, Version: db.Version, File: file}, func(protocol.Progress) {}); err != nil {
		return err
	}
	defer a.rmBackupsFile(context.WithoutCancel(ctx), file)
	id, err := a.helper(ctx, []mountInfo{backupsVol}, true)
	if err != nil {
		return err
	}
	defer a.docker.Action(context.WithoutCancel(ctx), protocol.ActionContainerRemove, id)
	return a.docker.copyFrom(ctx, id, "/orbit/backups/"+file, w)
}

func (a *Agent) rmBackupsFile(ctx context.Context, file string) {
	a.docker.Run(ctx, "orbit-backup-rm-"+randHex(), helperBody([]mountInfo{backupsVol}, false, []string{"rm", "-f", "/orbit/backups/" + file}))
}

// helperBody mounts each of the service's mounts at /orbit<destination>, so an archive of /orbit
// mirrors the container's own paths and a restore extracts straight back into them.
func helperBody(mounts []mountInfo, readOnly bool, cmd []string) map[string]any {
	ms := make([]map[string]any, 0, len(mounts))
	for _, m := range mounts {
		src := m.Name
		if m.Type == "bind" {
			src = m.Source
		}
		ms = append(ms, map[string]any{"Type": m.Type, "Source": src, "Target": "/orbit" + m.Destination, "ReadOnly": readOnly})
	}
	body := map[string]any{"Image": helperImage, "Labels": map[string]string{"orbit.backup-helper": "true"}, "HostConfig": map[string]any{"Mounts": ms}}
	if cmd != nil {
		body["Cmd"] = cmd
	}
	return body
}

func (a *Agent) helper(ctx context.Context, mounts []mountInfo, readOnly bool) (string, error) {
	if err := a.docker.EnsureImage(ctx, helperImage); err != nil {
		return "", err
	}
	return a.docker.CreateRaw(ctx, "orbit-backup-helper-"+randHex(), helperBody(mounts, readOnly, nil))
}

// serverRestore snapshots the service's current data, then replaces it from args.File.
func (a *Agent) serverRestore(ctx context.Context, args protocol.ServerRestoreArgs, progress func(protocol.Progress)) (protocol.ServerBackupResult, error) {
	res := protocol.ServerBackupResult{Backups: []protocol.ServiceBackup{}}
	if err := checkBackupArgs(args.ServerBackupArgs); err != nil || args.Only == "" || !fileRe.MatchString(args.File) {
		return res, errors.New("invalid restore arguments")
	}
	svcs, err := a.services(ctx)
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	i := slices.IndexFunc(svcs, func(s service) bool { return s.Key == args.Only })
	if i < 0 {
		res.Error = args.Only + " is not on this server"
		return res, nil
	}
	s, db := svcs[i], managedDB(args.ServerBackupArgs, args.Only)
	if db == nil && len(s.Mounts) == 0 {
		res.Error = args.Only + " has no volumes to restore into"
		return res, nil
	}
	step := stepper(progress)
	// The snapshot makes the restore undoable; without it nothing is touched.
	var snap protocol.ServiceBackup
	err = step("Snapshot of current data", func() error {
		snap = a.backupService(ctx, args.ServerBackupArgs, s)
		if snap.Error != "" {
			return errors.New(snap.Error)
		}
		return nil
	})
	res.Backups = append(res.Backups, snap)
	if err != nil {
		res.Error = "the snapshot before the restore failed, so nothing was changed: " + err.Error()
		return res, nil
	}
	if err := a.restoreService(ctx, args, s, db, step, progress); err != nil {
		res.Error = err.Error()
	}
	return res, nil
}

func (a *Agent) restoreService(ctx context.Context, args protocol.ServerRestoreArgs, s service, db *protocol.ServiceDB,
	step func(string, func() error) error, progress func(protocol.Progress)) error {
	unlock, err := lockService(s.Key)
	if err != nil {
		return err
	}
	defer unlock()
	plain, err := a.stageFile("restore.tar")
	if err != nil {
		return err
	}
	defer os.Remove(plain.Name())
	defer plain.Close()
	// The whole archive is decrypted and verified locally before any data is replaced.
	if err := step("Downloading and decrypting backup", func() error {
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(a.fetch(ctx, args.Dest, args.Folder, args.File, pw)) }()
		defer pr.Close()
		dec, err := backup.Decrypt(pr, args.Key)
		if err != nil {
			return err
		}
		gz, err := gzip.NewReader(dec)
		if err != nil {
			return err
		}
		if _, err := io.Copy(plain, gz); err != nil {
			return err
		}
		_, err = plain.Seek(0, io.SeekStart)
		return err
	}); err != nil {
		return err
	}
	if db != nil {
		return a.restoreDB(ctx, *db, plain, progress)
	}
	var stopped []string
	err = step("Stopping service", func() error {
		for _, c := range s.Containers {
			if c.State.Running {
				if err := a.docker.Action(ctx, protocol.ActionContainerStop, c.ID); err != nil {
					return err
				}
				stopped = append(stopped, c.ID)
			}
		}
		return nil
	})
	if err == nil {
		err = step("Replacing data", func() error {
			paths := []string{}
			for _, m := range s.Mounts {
				paths = append(paths, "/orbit"+m.Destination)
			}
			code, out, err := a.docker.Run(ctx, "orbit-backup-wipe-"+randHex(), helperBody(s.Mounts, false,
				append(append([]string{"find"}, paths...), "-xdev", "-mindepth", "1", "-delete")))
			if err == nil && code != 0 {
				err = fmt.Errorf("clearing the volumes failed:\n%s", tail(out, 10))
			}
			if err != nil {
				return err
			}
			id, err := a.helper(ctx, s.Mounts, false)
			if err != nil {
				return err
			}
			defer a.docker.Action(context.WithoutCancel(ctx), protocol.ActionContainerRemove, id)
			return a.docker.copyTo(ctx, id, "/", plain)
		})
	}
	// started again even when the restore failed, so the service doesn't stay down
	serr := step("Starting service", func() error {
		bg := context.WithoutCancel(ctx)
		var errs []error
		for _, cid := range stopped {
			errs = append(errs, a.docker.Action(bg, protocol.ActionContainerStart, cid))
		}
		a.proxy.restore(bg, a.docker) // zero-downtime services get a new bridge IP on restart
		return errors.Join(errs...)
	})
	return errors.Join(err, serr)
}

// restoreDB puts the archived dump into the orbit-backups volume and runs the existing pg_restore path.
func (a *Agent) restoreDB(ctx context.Context, db protocol.ServiceDB, plain *os.File, progress func(protocol.Progress)) error {
	h, err := tar.NewReader(plain).Next()
	if err != nil || !fileRe.MatchString(h.Name) || !strings.HasSuffix(h.Name, ".dump") {
		return errors.New("the backup does not contain a database dump")
	}
	if _, err := plain.Seek(0, io.SeekStart); err != nil {
		return err
	}
	id, err := a.helper(ctx, []mountInfo{backupsVol}, false)
	if err != nil {
		return err
	}
	defer a.docker.Action(context.WithoutCancel(ctx), protocol.ActionContainerRemove, id)
	if err := a.docker.copyTo(ctx, id, "/orbit/backups", plain); err != nil {
		return err
	}
	defer a.rmBackupsFile(context.WithoutCancel(ctx), h.Name)
	return a.pgRestore(ctx, protocol.BackupArgs{DB: db.DB, Version: db.Version, File: h.Name}, progress)
}

// preDeploySnapshot backs up an app's volumes before a deploy replaces its container. Progress and
// the result go to the snapshot's own operation (ReportID), so they land in the backup history.
func (a *Agent) preDeploySnapshot(ctx context.Context, app string, args protocol.ServerBackupArgs) error {
	if args.ReportID == "" {
		return errors.New("snapshot has no report id")
	}
	args.Kind, args.Only = protocol.BackupPreDeploy, "app/"+app
	res, err := a.serverBackup(ctx, args, func(p protocol.Progress) { p.ID = args.ReportID; a.sendNow(ctx, protocol.TypeProgress, p) })
	r := protocol.Result{ID: args.ReportID, OK: err == nil}
	r.Output, _ = json.Marshal(res)
	if err != nil {
		r.Error = err.Error()
	}
	a.sendNow(ctx, protocol.TypeResult, r)
	if err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	for _, b := range res.Backups {
		if b.Error != "" {
			return errors.New(b.Error)
		}
	}
	return nil
}

func (a *Agent) serverBackupPrune(ctx context.Context, args protocol.BackupPruneArgs) error {
	var errs []error
	for _, f := range args.Files {
		if fileRe.MatchString(f) {
			errs = append(errs, a.removeArchive(ctx, args.Dest, args.Folder, f))
		}
	}
	return errors.Join(errs...)
}

func s3Client(t *protocol.S3Target) backup.S3 {
	return backup.S3{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket, AccessKey: t.AccessKey, SecretKey: t.SecretKey}
}

func (a *Agent) store(ctx context.Context, d protocol.BackupDestination, folder, file string, f *os.File, size int64) (string, error) {
	if d.S3 != nil {
		key := backup.ObjectKey(d.S3.Prefix, folder, file)
		return "s3://" + d.S3.Bucket + "/" + key, s3Client(d.S3).Upload(ctx, key, f, size)
	}
	return a.sshPut(ctx, *d.Server, file, io.NewSectionReader(f, 0, size), size)
}

func (a *Agent) fetch(ctx context.Context, d protocol.BackupDestination, folder, file string, w io.Writer) error {
	if d.S3 != nil {
		return s3Client(d.S3).Download(ctx, backup.ObjectKey(d.S3.Prefix, folder, file), w)
	}
	return a.sshRun(ctx, *d.Server, "get "+file, nil, w)
}

func (a *Agent) removeArchive(ctx context.Context, d protocol.BackupDestination, folder, file string) error {
	if d.S3 != nil {
		return s3Client(d.S3).Delete(ctx, backup.ObjectKey(d.S3.Prefix, folder, file))
	}
	return a.sshRun(ctx, *d.Server, "rm "+file, nil, io.Discard)
}

// serverBackupCommand runs the server backup actions. Backups of large volumes outlast the usual
// 30-minute command limit, so they get their own.
func (a *Agent) serverBackupCommand(ctx context.Context, cmd protocol.Command, progress func(protocol.Progress)) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Hour)
	defer cancel()
	switch cmd.Action {
	case protocol.ActionServerBackup:
		var args protocol.ServerBackupArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return a.serverBackup(ctx, args, progress)
	case protocol.ActionServerRestore:
		var args protocol.ServerRestoreArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return a.serverRestore(ctx, args, progress)
	case protocol.ActionServerBackupPrune:
		var args protocol.BackupPruneArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return nil, a.serverBackupPrune(ctx, args)
	case protocol.ActionServerBackupKey:
		return a.backupPublicKey()
	case protocol.ActionServerBackupAuthorize:
		var args protocol.BackupAuthorizeArgs
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return nil, err
		}
		return a.backupAuthorize(ctx, args)
	}
	return nil, fmt.Errorf("unknown action %q", cmd.Action)
}

// copyFrom streams a tar of path in the container. Volumes are included even if it never started.
func (d *Docker) copyFrom(ctx context.Context, id, path string, w io.Writer) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/v1.43/containers/"+url.PathEscape(id)+"/archive?path="+url.QueryEscape(path), nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("docker archive %s: status %d", path, resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// copyTo extracts a tar into dir in the container, keeping the archived file owners.
func (d *Docker) copyTo(ctx context.Context, id, dir string, r io.Reader) error {
	req, _ := http.NewRequestWithContext(ctx, "PUT", "http://docker/v1.43/containers/"+url.PathEscape(id)+"/archive?copyUIDGID=1&path="+url.QueryEscape(dir), r)
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct{ Message string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("docker extract into %s: status %d %s", dir, resp.StatusCode, e.Message)
	}
	return nil
}
