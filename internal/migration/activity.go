package migration

import (
	"context"
	"time"
)

// maxLogEntries bounds the activity log kept in state.json.
const maxLogEntries = 200

// LogEntry is one line of the new host's activity log: what the Migration
// did, when, and for which item.
type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"` // info | warn | error
	Item    string    `json:"item,omitempty"`
	Message string    `json:"message"`
}

// FinalizeStep is where finalize is, for the pull TUI.
type FinalizeStep string

const (
	FinalizeStopTraefik  FinalizeStep = "stop-traefik"
	FinalizeMoveData     FinalizeStep = "move-data"
	FinalizeRestart      FinalizeStep = "restart"
	FinalizeStartTraefik FinalizeStep = "start-traefik"
	FinalizeDeploy       FinalizeStep = "deploy"
	FinalizeFinish       FinalizeStep = "finish"
)

type reporterKey struct{}

// WithFinalizeReporter makes the Finalizer report each step it reaches to
// report.
func WithFinalizeReporter(ctx context.Context, report func(FinalizeStep)) context.Context {
	return context.WithValue(ctx, reporterKey{}, report)
}

func reportStep(ctx context.Context, step FinalizeStep) {
	if report, ok := ctx.Value(reporterKey{}).(func(FinalizeStep)); ok {
		report(step)
	}
}

// record appends to the activity log, dropping the oldest entries past
// maxLogEntries. The caller holds t.mu and saves.
func (t *Target) record(level, item, message string) {
	t.st.Log = append(t.st.Log, LogEntry{At: time.Now().UTC(), Level: level, Item: item, Message: message})
	if n := len(t.st.Log) - maxLogEntries; n > 0 {
		t.st.Log = append([]LogEntry(nil), t.st.Log[n:]...)
	}
}

// logf records and saves.
func (t *Target) logf(level, item, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.record(level, item, message)
	t.save()
}

// reportFinalize records the step finalize reached.
func (t *Target) reportFinalize(step FinalizeStep) {
	messages := map[FinalizeStep]string{
		FinalizeStopTraefik:  "Stopping Traefik so it cannot overwrite the copied certificates",
		FinalizeMoveData:     "Moving the copied data directory into place",
		FinalizeRestart:      "Restarting the panel to swap in the migrated database",
		FinalizeStartTraefik: "Starting Traefik",
		FinalizeDeploy:       "Deploying the Composes that were running on the old server",
		FinalizeFinish:       "Telling the old server the migration finished",
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.st.FinalizeStep = step
	t.record("info", "", messages[step])
	t.save()
}
