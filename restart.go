package main

import "log"

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
