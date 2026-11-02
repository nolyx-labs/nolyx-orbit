// Server-to-server backups over SSH. The target's agent creates a restricted orbit-backups user whose
// only command is `orbit-agent backup-receive <source>`, which can put, get and remove archives in
// that source's own folder. The source signs in with its own ed25519 key (the private key never
// leaves it) and pins the target's host key.
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"orbit/internal/protocol"
)

const (
	receiveRoot = "/var/lib/orbit-backups"
	receiveUser = "orbit-backups"
)

var (
	keyMu      sync.Mutex
	safePathRe = regexp.MustCompile(`^/[A-Za-z0-9/._-]+$`)
)

// backupSigner loads this server's backup key, creating it on first use.
func (a *Agent) backupSigner() (ssh.Signer, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	path := filepath.Join(filepath.Dir(a.cfg.StateFile), "backup_ed25519")
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "orbit-backup")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

func (a *Agent) backupPublicKey() (map[string]string, error) {
	s, err := a.backupSigner()
	if err != nil {
		return nil, err
	}
	return map[string]string{"public_key": authorizedKey(s.PublicKey())}, nil
}

func authorizedKey(k ssh.PublicKey) string { return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) }

// pinnedConfig only accepts the host key recorded when the destination was set up.
func pinnedConfig(t protocol.SSHTarget, signer ssh.Signer) (*ssh.ClientConfig, error) {
	hk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(t.HostKey))
	if err != nil {
		return nil, fmt.Errorf("invalid pinned host key: %w", err)
	}
	return &ssh.ClientConfig{
		User: receiveUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		// the algorithm list makes the target present the pinned key's type rather than another of its keys
		HostKeyCallback: ssh.FixedHostKey(hk), HostKeyAlgorithms: []string{hk.Type()}, Timeout: 20 * time.Second,
	}, nil
}

func (a *Agent) sshRun(ctx context.Context, t protocol.SSHTarget, cmd string, stdin io.Reader, stdout io.Writer) error {
	signer, err := a.backupSigner()
	if err != nil {
		return err
	}
	return sshRun(ctx, t, signer, cmd, stdin, stdout)
}

func sshRun(ctx context.Context, t protocol.SSHTarget, signer ssh.Signer, cmd string, stdin io.Reader, stdout io.Writer) error {
	cfg, err := pinnedConfig(t, signer)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, "tcp", t.Addr)
	if err != nil {
		return err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, t.Addr, cfg)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh to %s: %w", t.Addr, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	var stderr bytes.Buffer
	sess.Stdin, sess.Stdout, sess.Stderr = stdin, stdout, &stderr
	if err := sess.Run(cmd); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("target server: %s", msg)
		}
		return err
	}
	return nil
}

// sshPut checks the target has room for the archive, then streams it.
func (a *Agent) sshPut(ctx context.Context, t protocol.SSHTarget, file string, r io.Reader, size int64) (string, error) {
	var out bytes.Buffer
	if err := a.sshRun(ctx, t, "df", nil, &out); err != nil {
		return "", err
	}
	free, _ := strconv.ParseInt(strings.TrimSpace(out.String()), 10, 64)
	if need := size + size/20 + 256<<20; free < need { // headroom so backups never fill the target's disk
		return "", fmt.Errorf("not enough free disk on the target server: %d MB free, %d MB needed", free>>20, need>>20)
	}
	if err := a.sshRun(ctx, t, fmt.Sprintf("put %s %d", file, size), r, io.Discard); err != nil {
		return "", err
	}
	host, _, _ := net.SplitHostPort(t.Addr)
	return fmt.Sprintf("%s:%s/%s/%s", host, receiveRoot, a.state.ServerID, file), nil
}

// BackupReceive is the forced command of the orbit-backups SSH user (orbit-agent backup-receive <source>).
func BackupReceive(sourceID string) int {
	if !uuidRe.MatchString(sourceID) {
		fmt.Fprintln(os.Stderr, "invalid source")
		return 2
	}
	if err := receive(filepath.Join(receiveRoot, sourceID), os.Getenv("SSH_ORIGINAL_COMMAND"), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// receive serves one request: "df", "put <file> <size>", "get <file>" or "rm <file>", inside dir only.
func receive(dir, cmd string, in io.Reader, out io.Writer) error {
	f := strings.Fields(cmd)
	if len(f) == 1 && f[0] == "df" {
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, uint64(st.Bavail)*uint64(st.Bsize))
		return err
	}
	if len(f) < 2 || !fileRe.MatchString(f[1]) || strings.HasPrefix(f[1], ".") {
		return errors.New("invalid command")
	}
	path := filepath.Join(dir, f[1])
	switch {
	case f[0] == "put" && len(f) == 3:
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < 0 {
			return errors.New("invalid size")
		}
		// written aside and renamed only when every byte arrived, so a dropped connection never leaves a partial archive
		tmp := filepath.Join(dir, "."+f[1]+".part")
		w, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		n, err := io.Copy(w, io.LimitReader(in, size+1))
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err == nil && n != size {
			err = fmt.Errorf("received %d of %d bytes", n, size)
		}
		if err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, path)
	case f[0] == "get" && len(f) == 2:
		r, err := os.Open(path)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = io.Copy(out, r)
		return err
	case f[0] == "rm" && len(f) == 2:
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return errors.New("invalid command")
}

// updateAuthorizedKeys replaces the source's line (or removes it when line is empty), keeping the others.
func updateAuthorizedKeys(existing, sourceID, line string) string {
	var out []string
	for _, l := range strings.Split(existing, "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasSuffix(l, " orbit-source-"+sourceID) {
			out = append(out, l)
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// backupAuthorize prepares this server to receive a source server's archives (or revokes it).
func (a *Agent) backupAuthorize(ctx context.Context, args protocol.BackupAuthorizeArgs) (protocol.BackupAuthorizeResult, error) {
	var res protocol.BackupAuthorizeResult
	if !uuidRe.MatchString(args.SourceID) {
		return res, errors.New("invalid source server")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil || a.cfg.HostRoot != "" || runtime.GOOS != "linux" {
		return res, errors.New("receiving backups needs the agent installed on the host with the install script, not the agent container")
	}
	line := ""
	if args.PublicKey != "" {
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(args.PublicKey))
		if err != nil || pk.Type() != ssh.KeyAlgoED25519 {
			return res, errors.New("invalid source public key")
		}
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil || !safePathRe.MatchString(exe) {
			return res, errors.New("cannot determine the agent's path for the forced command")
		}
		line = fmt.Sprintf(`restrict,command="%s backup-receive %s" %s orbit-source-%s`, exe, args.SourceID, authorizedKey(pk), args.SourceID)
	}
	if _, err := user.Lookup(receiveUser); err != nil {
		// a real shell is needed for sshd to run the forced command; "*" keeps password login impossible
		// while key login works (a "!"-locked account is refused key login when sshd doesn't use PAM)
		for _, c := range [][]string{
			{"useradd", "--system", "--no-create-home", "--home-dir", receiveRoot, "--shell", "/bin/sh", receiveUser},
			{"usermod", "-p", "*", receiveUser},
		} {
			if out, err := exec.CommandContext(ctx, c[0], c[1:]...).CombinedOutput(); err != nil {
				return res, fmt.Errorf("%s: %v: %s", c[0], err, bytes.TrimSpace(out))
			}
		}
	}
	u, err := user.Lookup(receiveUser)
	if err != nil {
		return res, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	// The home and .ssh stay root-owned so the backup user can't change its own keys; it owns only
	// the per-source folders.
	dir := filepath.Join(receiveRoot, args.SourceID)
	for _, d := range []string{receiveRoot, filepath.Join(receiveRoot, ".ssh")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return res, err
		}
		if err := os.Chmod(d, 0o755); err != nil {
			return res, err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return res, err
	}
	ak := filepath.Join(receiveRoot, ".ssh", "authorized_keys")
	old, _ := os.ReadFile(ak)
	if err := os.WriteFile(ak+".new", []byte(updateAuthorizedKeys(string(old), args.SourceID, line)), 0o644); err != nil {
		return res, err
	}
	if err := os.Rename(ak+".new", ak); err != nil {
		return res, err
	}
	b, err := os.ReadFile("/etc/ssh/ssh_host_ed25519_key.pub")
	if err != nil {
		return res, errors.New("this server has no SSH ed25519 host key: is OpenSSH server installed?")
	}
	hk, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return res, err
	}
	res.HostKey = authorizedKey(hk)
	var st syscall.Statfs_t
	if syscall.Statfs(receiveRoot, &st) == nil {
		res.FreeBytes = int64(uint64(st.Bavail) * uint64(st.Bsize))
	}
	return res, nil
}
