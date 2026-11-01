package local_test

import (
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
)

// socketDir is short: a Unix socket path must stay under ~104 bytes.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "zl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestTheSocketIsOnlyOpenToItsOwnerByDefault(t *testing.T) {
	path := filepath.Join(socketDir(t), "node.sock")
	l, err := local.Listen(path, local.ListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&fs.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v", fi.Mode())
	}
	if l.Addr().Network() != "unix" {
		t.Fatalf("network = %s", l.Addr().Network())
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("left behind %v", entries)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket still there after close: %v", err)
	}
}

func TestTheSocketCanBeOpenedToAGroup(t *testing.T) {
	path := filepath.Join(socketDir(t), "node.sock")
	l, err := local.Listen(path, local.ListenOptions{Mode: 0o660, GID: os.Getgid()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode = %v", fi.Mode())
	}
}

func TestTheSocketIsNeverOpenToOthers(t *testing.T) {
	for _, mode := range []fs.FileMode{0o666, 0o604, 0o601, 0o602} {
		if l, err := local.Listen(filepath.Join(socketDir(t), "node.sock"), local.ListenOptions{Mode: mode}); err == nil {
			_ = l.Close()
			t.Errorf("mode %#o accepted", mode)
		}
	}
}

func TestAStaleSocketIsReplacedButNoOtherFile(t *testing.T) {
	dir := socketDir(t)
	path := filepath.Join(dir, "node.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	l, err := local.Listen(path, local.ListenOptions{})
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	_ = l.Close()

	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := local.Listen(file, local.ListenOptions{}); err == nil {
		_ = l.Close()
		t.Fatal("listening replaced a regular file")
	}
	if b, _ := os.ReadFile(file); string(b) != "keep" {
		t.Fatal("regular file changed")
	}
}
