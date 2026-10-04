package terminal_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/terminal"
	"relist-panel/internal/terminal/faketest"
)

// nonProbeKills lists the signals of the recorded kills other than the "0"
// liveness probe, for one pid.
func nonProbeKills(exec *faketest.Exec, pid int) []string {
	var signals []string
	for _, kill := range exec.KillsSnapshot() {
		if kill.PID == pid && kill.Signal != "0" {
			signals = append(signals, kill.Signal)
		}
	}
	return signals
}

func discardLog(string, ...any) {}

// Sweep hangs up every marked shell a crashed panel left running the way a
// session does: HUP, the grace, a probe, and KILL only for a shell still alive.
// A failed listing is logged through logf, never returned, and never stops the
// orphans it did find from being hung up.
func TestSweepKillsAnOrphanThatIgnoresHUP(t *testing.T) {
	exec := &faketest.Exec{Orphans: []terminal.OrphanShell{{ContainerID: "web", PID: 7}}}

	terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, time.Millisecond, discardLog)

	if got, want := nonProbeKills(exec, 7), []string{"HUP", "KILL"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kills = %v, want %v", got, want)
	}
}

func TestSweepStopsAtHUPWhenItWorks(t *testing.T) {
	exec := &faketest.Exec{
		Orphans:   []terminal.OrphanShell{{ContainerID: "web", PID: 7}},
		KillExits: map[int]bool{7: true},
	}

	terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, time.Millisecond, discardLog)

	if got, want := nonProbeKills(exec, 7), []string{"HUP"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kills = %v, want %v", got, want)
	}
}

func TestSweepHangsUpOrphansConcurrently(t *testing.T) {
	exec := &faketest.Exec{Orphans: []terminal.OrphanShell{
		{ContainerID: "web", PID: 7},
		{ContainerID: "web", PID: 8},
		{ContainerID: "db", PID: 9},
	}}

	start := time.Now()
	terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, 200*time.Millisecond, discardLog)

	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Errorf("Sweep took %v, want under 500ms for three orphans on a 200ms grace", elapsed)
	}
	for _, pid := range []int{7, 8, 9} {
		if got, want := nonProbeKills(exec, pid), []string{"HUP", "KILL"}; !reflect.DeepEqual(got, want) {
			t.Errorf("pid %d kills = %v, want %v", pid, got, want)
		}
	}
}

func TestSweepHangsUpWhatItFoundDespiteAListingError(t *testing.T) {
	exec := &faketest.Exec{
		Orphans:         []terminal.OrphanShell{{ContainerID: "web", PID: 7}},
		OrphanShellsErr: errors.New("daemon boom"),
		KillExits:       map[int]bool{7: true},
	}
	var logs []string

	terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, time.Millisecond, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	if got, want := nonProbeKills(exec, 7), []string{"HUP"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kills = %v, want %v", got, want)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "daemon boom") {
		t.Errorf("logs = %v, want the listing error logged once", logs)
	}
}

func TestSweepLogsAListingErrorWithNothingFound(t *testing.T) {
	exec := &faketest.Exec{OrphanShellsErr: errors.New("daemon boom")}
	var logs []string

	terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, time.Millisecond, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	if len(logs) == 0 || !strings.Contains(logs[0], "daemon boom") {
		t.Fatalf("logs = %v, want the OrphanShells error logged", logs)
	}
	if got := exec.KillsSnapshot(); len(got) != 0 {
		t.Errorf("kills = %+v, want none", got)
	}
}
