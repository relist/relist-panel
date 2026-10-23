package migration

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenControlReplacesAStaleSocket(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	path := filepath.Join(dir, "control.sock")
	// A previous run's directory and socket file.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := ListenControl(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial the control socket: %v", err)
	}
	c.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want a 0600 socket", info.Mode())
	}
}

func TestListenControlCreatesAPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "relist-panel")
	l, err := ListenControl(filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, want 0700", d.Mode().Perm())
	}
}

func TestListenControlLeavesAnExistingDirectoryAlone(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	l, err := ListenControl(filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o755 {
		t.Fatalf("an existing directory was changed to %v", d.Mode().Perm())
	}
}
