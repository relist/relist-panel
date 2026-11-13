// Package datadirlock makes sure only one panel server runs on a data
// directory. Without it, a second server started by mistake (a bare
// `relist-panel` typed in the panel container, say) migrated panel.db and
// swept terminal shells before failing to listen.
package datadirlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FileName is the lock file inside the data directory.
const FileName = ".panel.lock"

// ErrHeld means another live process holds the lock on the data directory.
var ErrHeld = errors.New("another panel server is already running on this data directory")

// Lock is a held lock. The kernel drops it when the process exits or
// re-executes (Go opens files close-on-exec), so a crash never leaves it held.
type Lock struct {
	f *os.File
}

// Acquire takes the lock on dir without waiting, creating dir if needed.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (%s)", ErrHeld, dir)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. The file stays; only a live holder counts.
func (l *Lock) Release() error {
	return l.f.Close()
}
