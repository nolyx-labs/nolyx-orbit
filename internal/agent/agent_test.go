package agent

import (
	"crypto/ed25519"
	"testing"
	"time"

	"orbit/internal/protocol"
)

func TestAcceptRejectsReplayExpiryForgeryAndUnknownActions(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	a := &Agent{seen: newSeenSet(2), state: state{ServerID: "srv-1", SigningKey: pub}}
	now := time.Now()
	sign := func(c protocol.Command, server string) protocol.Command {
		c.Signature = ed25519.Sign(priv, protocol.SigningPayload(server, c))
		return c
	}
	ok := sign(protocol.Command{ID: "c1", Action: protocol.ActionContainerRestart, Args: []byte(`{"container_id":"x"}`), ExpiresAt: now.Add(time.Minute)}, "srv-1")
	if err := a.accept(ok, now); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	if a.accept(ok, now) == nil {
		t.Fatal("replayed command accepted")
	}
	if a.accept(sign(protocol.Command{ID: "c2", Action: "shell.exec", ExpiresAt: now.Add(time.Minute)}, "srv-1"), now) == nil {
		t.Fatal("unknown action accepted")
	}
	if a.accept(sign(protocol.Command{ID: "c3", Action: protocol.ActionDeploy, ExpiresAt: now.Add(-time.Second)}, "srv-1"), now) == nil {
		t.Fatal("expired command accepted")
	}
	if a.accept(sign(protocol.Command{ID: "c4", Action: protocol.ActionDeploy, ExpiresAt: now.Add(time.Minute)}, "srv-2"), now) == nil {
		t.Fatal("command signed for another server accepted")
	}
	tampered := sign(protocol.Command{ID: "c5", Action: protocol.ActionContainerStop, Args: []byte(`{"container_id":"a"}`), ExpiresAt: now.Add(time.Minute)}, "srv-1")
	tampered.Args = []byte(`{"container_id":"b"}`)
	if a.accept(tampered, now) == nil {
		t.Fatal("tampered args accepted")
	}
}

func TestFramesAndRedact(t *testing.T) {
	raw := append([]byte{1, 0, 0, 0, 0, 0, 0, 3}, "ab\n"...)
	raw = append(raw, append([]byte{2, 0, 0, 0, 0, 0, 0, 4}, "err\n"...)...)
	fs := frames(raw)
	if len(fs) != 2 || fs[0].stream != 1 || string(fs[1].data) != "err\n" {
		t.Fatalf("frames = %+v", fs)
	}
	if got := redact("cloning https://x-access-token:ghp_secret@github.com/a/b.git"); got != "cloning https://***@github.com/a/b.git" {
		t.Fatalf("redact = %q", got)
	}
}

func TestDemux(t *testing.T) {
	raw := append([]byte{1, 0, 0, 0, 0, 0, 0, 6}, "hello\n"...)
	raw = append(raw, append([]byte{2, 0, 0, 0, 0, 0, 0, 4}, "err\n"...)...)
	if got := demux(raw); got != "hello\nerr\n" {
		t.Fatalf("demux = %q", got)
	}
	if got := demux([]byte("tty output\n")); got != "tty output\n" {
		t.Fatalf("tty demux = %q", got)
	}
}
