package panelupdate

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
)

// replaceFile writes data to path atomically: a synced temp file in the same
// directory, then a rename. An existing file keeps its mode and owner, so the
// operator's own file does not turn into root's. mode is for a new file.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	var uid, gid = -1, -1
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".panel-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if uid >= 0 {
		_ = tmp.Chown(uid, gid) // best effort: only root can give a file away
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// restoreFile puts data back at path unless it is already there.
func restoreFile(path string, data []byte, mode os.FileMode) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	return replaceFile(path, data, mode)
}

// copyFileAtomic copies src over dst through a temp file and a rename.
func copyFileAtomic(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return replaceFile(dst, data, mode)
}
