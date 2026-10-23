package main

import (
	"log"
	"time"
)

// reexec replaces this process with a fresh run of exe under the same PID, so
// the container does not restart: a `docker exec` session into it, such as
// the migration TUI, survives finalize swapping in the migrated database
// (ADR-0019). If exec fails, exit(1) leaves it to the restart policy.
func reexec(exe string, args, env []string, exec func(string, []string, []string) error, exit func(int)) {
	if err := exec(exe, args, env); err != nil {
		log.Printf("migration: re-execute the panel: %v; exiting for the restart policy", err)
		exit(1)
	}
}

// restartAfter shuts the panel down, closes the database and re-executes.
// A shutdown still running after grace is left behind, so a hung wait can
// never leave the panel neither re-executed nor exited, with HTTP down and
// finalize's marker written; exec replaces the process anyway.
func restartAfter(grace time.Duration, shutdown func(), closeDB func() error, exec func()) {
	done := make(chan struct{})
	go func() {
		shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		log.Printf("migration: the shutdown is still running after %s; re-executing anyway", grace)
	}
	if err := closeDB(); err != nil {
		log.Printf("close the database: %v", err)
	}
	exec()
}
