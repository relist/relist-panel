package terminal

import (
	"context"
	"sync"
	"time"
)

// Sweep is the panel's startup backstop for the shells a crash leaves behind:
// the in-process hang-up only runs if the panel lives to see the session end,
// so at boot it asks the Exec seam for marked shells still running and hangs
// each one up the way a session does (HUP, grace, probe, KILL), one goroutine
// per orphan. A listing that failed part-way still hangs up what it found.
// logf receives the listing error and any hang-up that failed; Sweep returns
// nothing, because a failed sweep must never stop the panel from starting.
func Sweep(ctx context.Context, exec Exec, projects []string, grace time.Duration, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	orphans, err := exec.OrphanShells(ctx, projects)
	if err != nil {
		logf("terminal: could not sweep orphaned shells completely: %v", err)
	}

	var wg sync.WaitGroup
	for _, orphan := range orphans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := hangUp(ctx, exec, orphan.ContainerID, orphan.PID, grace); err != nil {
				logf("terminal: could not hang up orphaned shell %d in container %s: %v", orphan.PID, orphan.ContainerID, err)
			}
		}()
	}
	wg.Wait()
}
