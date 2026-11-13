package datadirlock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAcquireCreatesTheDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("lock file: %v", err)
	}
}

func TestSecondAcquireOnTheSameDirIsRefused(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	second, err := Acquire(dir)
	if !errors.Is(err, ErrHeld) {
		if second != nil {
			second.Release()
		}
		t.Fatalf("second Acquire err = %v, want ErrHeld", err)
	}
}

func TestAcquireSucceedsAfterRelease(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	second.Release()
}

func TestOtherDirsAreIndependent(t *testing.T) {
	a, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	defer a.Release()
	b, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	b.Release()
}

func TestTheLockIsDroppedOnReexec(t *testing.T) {
	// A finished server Migration re-executes the panel in the same process
	// (restart.go); the new image must be able to take the lock again, so the
	// descriptor must close on exec.
	l, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, l.f.Fd(), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatalf("fcntl: %v", errno)
	}
	if flags&syscall.FD_CLOEXEC == 0 {
		t.Fatal("lock descriptor survives exec; a re-executed panel would refuse to start")
	}
}

func TestALeftoverLockFileDoesNotBlock(t *testing.T) {
	// A panel killed with SIGKILL leaves the file behind; only a live holder
	// of the lock counts.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire with a leftover file: %v", err)
	}
	l.Release()
}
