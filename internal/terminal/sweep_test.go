package terminal_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/terminal"
	"relist-panel/internal/terminal/faketest"
)

// Sweep hangs up every marked shell a crashed panel left running, and never
// lets a failed listing abort startup: an OrphanShells error is logged through
// logf, not returned.
func TestSweepHangsUpEveryOrphan(t *testing.T) {
	t.Run("hangs up every orphan", func(t *testing.T) {
		exec := &faketest.Exec{Orphans: []terminal.OrphanShell{
			{ContainerID: "web", PID: 7},
			{ContainerID: "web", PID: 9},
		}}
		var logs []string

		terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		})

		want := []faketest.KillCall{
			{ContainerID: "web", PID: 7, Signal: "HUP"},
			{ContainerID: "web", PID: 9, Signal: "HUP"},
		}
		if got := exec.KillsSnapshot(); !reflect.DeepEqual(got, want) {
			t.Errorf("kills = %+v, want %+v", got, want)
		}
		if len(logs) != 0 {
			t.Errorf("Sweep logged %v on success, want nothing", logs)
		}
	})

	t.Run("logs an OrphanShells error instead of returning it", func(t *testing.T) {
		exec := &faketest.Exec{OrphanShellsErr: errors.New("daemon boom")}
		var logs []string

		terminal.Sweep(context.Background(), exec, []string{"relist-c3"}, func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		})

		if len(logs) == 0 {
			t.Fatal("Sweep logged nothing, want the OrphanShells error logged")
		}
		if !strings.Contains(logs[0], "daemon boom") {
			t.Errorf("logged %q, want it to name the OrphanShells error", logs[0])
		}
		if got := exec.KillsSnapshot(); len(got) != 0 {
			t.Errorf("kills = %+v, want none after a failed listing", got)
		}
	})
}
