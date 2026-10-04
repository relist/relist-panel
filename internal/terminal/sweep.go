package terminal

import "context"

// Sweep is the panel's startup backstop for the shells a crash leaves behind:
// the in-process hang-up only runs if the panel lives to see the session end,
// so at boot it asks the Exec seam for marked shells still running and hangs
// them up. It hangs up every orphan with one "HUP" each. logf receives the
// reason a listing failed and any hang-up that failed; Sweep returns nothing,
// because a failed sweep must never stop the panel from starting.
func Sweep(ctx context.Context, exec Exec, projects []string, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	orphans, err := exec.OrphanShells(ctx, projects)
	if err != nil {
		logf("terminal: could not sweep orphaned shells: %v", err)
		return
	}
	for _, orphan := range orphans {
		if err := exec.Kill(ctx, orphan.ContainerID, orphan.PID, "HUP"); err != nil {
			logf("terminal: could not hang up orphaned shell %d in container %s: %v", orphan.PID, orphan.ContainerID, err)
		}
	}
}
