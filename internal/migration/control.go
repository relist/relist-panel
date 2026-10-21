package migration

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
)

// ListenControl listens on the control socket at path. A missing directory
// is created 0700; an existing one is left as it is. A stale socket file from
// a previous run, or the run this one re-executed, is replaced. The socket
// itself is 0600: reaching it means running a command in the container.
func ListenControl(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
