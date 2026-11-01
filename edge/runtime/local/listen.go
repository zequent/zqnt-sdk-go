package local

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
)

// DefaultSocketMode lets only the node's own user connect.
const DefaultSocketMode fs.FileMode = 0o600

// ListenOptions configure the node socket.
type ListenOptions struct {
	// Mode is the socket's permission bits (DefaultSocketMode). 0660 admits the socket's group;
	// any permission for others is refused.
	Mode fs.FileMode
	// GID, when positive, becomes the socket's group, so adapters running as another user of that
	// group can connect with Mode 0660.
	GID int
}

type unixListener struct {
	*net.UnixListener
	path string
}

func (l *unixListener) Close() error {
	err := l.UnixListener.Close()
	if rmErr := os.Remove(l.path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		err = errors.Join(err, rmErr)
	}
	return err
}

// Listen opens the node socket at path. The socket gets its mode and group before it appears at
// path, so no other user can connect in between: it is bound in a fresh private directory next to
// path and renamed into place. A stale socket at path is replaced; any other file there is an
// error. Closing the listener removes the socket.
func Listen(path string, o ListenOptions) (net.Listener, error) {
	mode := o.Mode
	if mode == 0 {
		mode = DefaultSocketMode
	}
	if mode&0o007 != 0 || mode&^fs.ModePerm != 0 {
		return nil, fmt.Errorf("local: socket mode %#o: only owner and group may have access", mode)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("local: socket path: %w", err)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSocket == 0 {
		return nil, fmt.Errorf("local: %s exists and is not a socket", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("local: socket directory: %w", err)
	}
	private, err := os.MkdirTemp(dir, ".zqnt-sock-")
	if err != nil {
		return nil, fmt.Errorf("local: socket directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(private) }()

	bound := filepath.Join(private, "s")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: bound, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("local: listen on %s: %w", path, err)
	}
	l.SetUnlinkOnClose(false)
	fail := func(err error) (net.Listener, error) {
		_ = l.Close()
		return nil, fmt.Errorf("local: listen on %s: %w", path, err)
	}
	if o.GID > 0 {
		if err := os.Chown(bound, -1, o.GID); err != nil {
			return fail(err)
		}
	}
	if err := os.Chmod(bound, mode); err != nil {
		return fail(err)
	}
	if err := os.Rename(bound, path); err != nil {
		return fail(err)
	}
	return &unixListener{UnixListener: l, path: path}, nil
}
