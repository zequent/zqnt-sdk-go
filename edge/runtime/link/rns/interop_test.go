//go:build interop

package rns_test

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/rns"
)

// TestInteropThroughATransportNode runs a Python Reticulum hub (rnsd with transport enabled) and a
// Python peer, connects the Go driver to the hub, and checks that each side validates the other's
// announce and that an encrypted packet goes Go -> peer and the peer's echo comes back, both across
// the transport node. The Python side is only run, as a black box. Needs uv:
//
//	RNS_VERSION=0.9.4 go test -tags interop -run Interop ./edge/runtime/link/rns/
//
// RNS_VERSION selects the PyPI rns version for hub and peer (default 0.9.4).
func TestInteropThroughATransportNode(t *testing.T) {
	version := os.Getenv("RNS_VERSION")
	if version == "" {
		version = "0.9.4"
	}
	dir := t.TempDir()
	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	hubDir := filepath.Join(dir, "hub")
	mustWrite(t, hubDir, fmt.Sprintf(`[reticulum]
  enable_transport = Yes
  share_instance = No
[logging]
  loglevel = 2
[interfaces]
  [[server]]
    type = TCPServerInterface
    interface_enabled = Yes
    listen_ip = 127.0.0.1
    listen_port = %d
`, port))
	hub := withRNS(version, "rnsd", "--config", hubDir)
	hub.Stdout, hub.Stderr = os.Stderr, os.Stderr
	start(t, hub)
	waitForPort(t, ctx, port)
	t.Cleanup(func() {
		if t.Failed() {
			if b, err := os.ReadFile(filepath.Join(hubDir, "logfile")); err == nil {
				t.Logf("hub log:\n%s", b)
			}
		}
	})

	peerDir := filepath.Join(dir, "peer")
	if err := os.MkdirAll(peerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := withRNS(version, "python", "-u", "testdata/interop/peer.py", peerDir, fmt.Sprint(port))
	out, _ := peer.StdoutPipe()
	peer.Stderr = os.Stderr
	start(t, peer)
	lines := make(chan string, 64)
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			t.Log("peer: " + s.Text())
			lines <- s.Text()
		}
	}()
	var peerHash, peerVersion string
	for peerHash == "" {
		select {
		case l := <-lines:
			if f := strings.Fields(l); len(f) == 4 && f[0] == "PEER" {
				peerHash, peerVersion = f[1], f[3]
			}
		case <-ctx.Done():
			t.Fatal("peer did not start")
		}
	}

	d, err := link.New(link.LinkConfig{ID: "rns", Driver: rns.DriverID, Options: map[string]any{
		"address":           fmt.Sprintf("127.0.0.1:%d", port),
		"identity":          filepath.Join(dir, "go.identity"),
		"announce_interval": "2s",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Open(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	driver := d.(*rns.Driver)
	t.Logf("go node %s, peer %s on rns %s", driver.Address(), peerHash, peerVersion)

	want, _ := hex.DecodeString(peerHash)
	for {
		if p, ok := driver.Transport().PathTo(want); ok {
			t.Logf("go validated the peer's announce: %d hops, next hop %x", p.Hops, p.NextHop)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("go never validated the peer's announce")
		case <-time.After(200 * time.Millisecond):
		}
	}

	message := []byte("hello from go")
	deadline := time.After(45 * time.Second)
	retry := time.NewTicker(3 * time.Second)
	defer retry.Stop()
	send := func() {
		if r := d.Send(ctx, message, link.SendMeta{To: peerHash}); r.Status != link.Sent {
			t.Logf("send: %s %s", r.Status, r.Reason)
		}
	}
	send()
	for {
		select {
		case f := <-d.Receive():
			if string(f.Payload) != "echo:"+string(message) {
				t.Fatalf("unexpected frame %q", f.Payload)
			}
			t.Logf("go received the peer's encrypted echo after %d hops", f.Hops)
			return
		case l := <-lines:
			if strings.HasPrefix(l, "PEER_NO_GO_PATH") {
				t.Log("peer has no path to go yet, retrying")
			}
		case <-retry.C:
			send()
		case <-deadline:
			t.Fatal("no echo from the peer")
		}
	}
}

func mustWrite(t *testing.T, dir, config string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withRNS(version string, args ...string) *exec.Cmd {
	return exec.Command("uv", append([]string{"run", "--no-project", "--with", "rns==" + version}, args...)...)
}

// start runs cmd in its own process group and kills the whole group at cleanup, since uv leaves
// the Python process running when only uv is killed.
func start(t *testing.T, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, ctx context.Context, port int) {
	for ctx.Err() == nil {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("hub did not listen")
}
