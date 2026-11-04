package traefikconfig

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Kind is what a Traefik apply does.
type Kind string

const (
	KindApply   Kind = "apply"   // a new Traefik override
	KindRestore Kind = "restore" // a history entry's override
	KindRestart Kind = "restart" // the same files
)

// States of a run, in apply.json.
const (
	StateValidating     = "validating"
	StateDryRun         = "dry-run"
	StateWriting        = "writing"
	StateRestarting     = "restarting"
	StateChecking       = "checking"
	StateOK             = "ok"
	StateRollingBack    = "rolling-back"
	StateRolledBack     = "rolled-back"
	StateRollbackFailed = "rollback-failed"
	StateRefused        = "refused"
)

// logTail is how much of Traefik's log a run keeps.
const logTail = 500

var (
	ErrBusy     = errors.New("a Traefik apply is already running")
	ErrRevision = errors.New("the Traefik override changed since it was read")
)

// ProblemsError is a Traefik override refused before anything ran.
type ProblemsError struct{ Problems []Problem }

func (e *ProblemsError) Error() string { return "the Traefik override cannot be used: " + describe(e.Problems) }

// Run is the last Traefik apply, kept in apply.json. Healthy* carry over from
// run to run: the generated file Traefik last started from, healthy.
type Run struct {
	ID               string     `json:"id"`
	Kind             Kind       `json:"kind"`
	State            string     `json:"state"`
	Reason           string     `json:"reason,omitempty"`
	Backup           string     `json:"backup,omitempty"`
	Log              string     `json:"log,omitempty"`
	BootstrapWarning string     `json:"bootstrapWarning,omitempty"`
	StartedAt        time.Time  `json:"startedAt"`
	EndedAt          *time.Time `json:"endedAt,omitempty"`
	Acked            bool       `json:"acked"`
	EffectiveSHA256  string     `json:"effectiveSha256,omitempty"`
	TraefikStartedAt *time.Time `json:"traefikStartedAt,omitempty"`

	RestartCountBefore int        `json:"restartCountBefore"`
	RestartedAt        *time.Time `json:"restartedAt,omitempty"`
	HealthySHA256      string     `json:"healthySha256,omitempty"`
}

// Terminal reports whether a run in state s is over.
func Terminal(s string) bool {
	switch s {
	case StateOK, StateRolledBack, StateRollbackFailed, StateRefused, "":
		return true
	}
	return false
}

// Runner is the Traefik apply (ADR-0030): validate, dry-run, back up, write,
// restart, watch, and roll back when Traefik is not healthy.
type Runner struct {
	Paths   Paths
	Traefik Traefik
	Domain  string // RELIST_DOMAIN, for /healthz through Traefik
	// Gate refuses a run while a Panel update or the Migration lock is on.
	Gate  func(ctx context.Context) error
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	DryRunWait, WatchFor, Every time.Duration // 5 s, 60 s, 2 s when zero
	Keep                        int           // history entries; 10 when zero

	mu      sync.Mutex
	busy    bool
	done    chan struct{}
	onState func(string) // tests watch the states
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Revision is the hex sha256 of a Traefik override: what an apply must have
// read, so it cannot overwrite a newer one.
func Revision(override []byte) string { return sha(override) }

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.EncodeToString(b))
}

// Status is the last run, with the last traefik-config warning.
func (r *Runner) Status() (Run, error) {
	b, err := os.ReadFile(r.Paths.ApplyFile())
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, nil
	}
	if err != nil {
		return Run{}, err
	}
	var run Run
	if err := json.Unmarshal(b, &run); err != nil {
		return Run{}, fmt.Errorf("read %s: %w", r.Paths.ApplyFile(), err)
	}
	return run, nil
}

// save writes run to apply.json, keeping the bootstrap warning traefik-config
// wrote there.
func (r *Runner) save(run Run) error {
	if old, err := r.Status(); err == nil && run.BootstrapWarning == "" {
		run.BootstrapWarning = old.BootstrapWarning
	}
	b, err := json.Marshal(run)
	if err != nil {
		return err
	}
	return WriteAtomic(r.Paths.ApplyFile(), b)
}

// Busy reports whether a run is going.
func (r *Runner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy
}

// WaitIdle waits for a running apply to end; a Panel update's quiesce waits
// on it as it does on the Deployment queue.
func (r *Runner) WaitIdle(ctx context.Context) error {
	r.mu.Lock()
	if !r.busy {
		r.mu.Unlock()
		return nil
	}
	done := r.done
	r.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ack marks the run id seen, so its result is not shown again.
func (r *Runner) Ack(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.Status()
	if err != nil || run.ID != id {
		return err
	}
	run.Acked = true
	return r.save(run)
}

// Start checks an apply (the gate, the revision, the override) and runs the
// rest in the background. A restart needs neither override nor revision.
func (r *Runner) Start(ctx context.Context, kind Kind, override []byte, revision string) (string, error) {
	if kind == KindRestart {
		return r.begin(ctx, kind, nil, nil)
	}
	current, err := os.ReadFile(r.Paths.Override())
	switch {
	case errors.Is(err, os.ErrNotExist):
		if revision != "" && revision != Revision(nil) {
			return "", ErrRevision
		}
	case err != nil:
		return "", err
	case Revision(current) != revision:
		return "", ErrRevision
	}
	effective, err := generateOrProblems(override)
	if err != nil {
		return "", err
	}
	return r.begin(ctx, kind, override, effective)
}

func generateOrProblems(override []byte) ([]byte, error) {
	effective, problems, err := Generate(override)
	if err != nil {
		return nil, &ProblemsError{Problems: []Problem{ParseErrorProblem(err)}}
	}
	if len(problems) > 0 {
		return nil, &ProblemsError{Problems: problems}
	}
	return effective, nil
}

// begin takes the run slot and starts the run.
func (r *Runner) begin(ctx context.Context, kind Kind, override, effective []byte) (string, error) {
	if r.Gate != nil {
		if err := r.Gate(ctx); err != nil {
			return "", err
		}
	}
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return "", ErrBusy
	}
	prev, _ := r.Status()
	run := Run{ID: newID(), Kind: kind, State: StateValidating, StartedAt: r.now().UTC(), HealthySHA256: prev.HealthySHA256}
	if err := r.save(run); err != nil {
		r.mu.Unlock()
		return "", err
	}
	r.busy, r.done = true, make(chan struct{})
	r.mu.Unlock()
	go r.run(context.WithoutCancel(ctx), run, override, effective)
	return run.ID, nil
}

// step records a state change before the step runs.
func (r *Runner) step(run *Run, state string) {
	run.State = state
	if err := r.save(*run); err != nil {
		log.Printf("traefikconfig: save run %s: %v", run.ID, err)
	}
	if r.onState != nil {
		r.onState(state)
	}
}

// finish ends a run in a terminal state and frees the slot.
func (r *Runner) finish(run *Run, state, reason string) {
	ended := r.now().UTC()
	run.EndedAt, run.Reason = &ended, reason
	r.step(run, state)
	if run.Backup != "" {
		r.recordResult(run.Backup, state)
	}
	r.mu.Lock()
	r.busy = false
	close(r.done)
	r.mu.Unlock()
}

func (r *Runner) run(ctx context.Context, run Run, override, effective []byte) {
	if run.Kind != KindRestart {
		r.step(&run, StateDryRun)
		if reason, logText := r.dryRun(ctx, run.ID, effective); reason != "" {
			run.Log = logText
			r.finish(&run, StateRefused, reason)
			return
		}
		name, err := r.backup(run.ID, run.Kind)
		if err != nil {
			r.finish(&run, StateRefused, "could not back up the current files: "+err.Error())
			return
		}
		run.Backup = name
		r.step(&run, StateWriting)
		if err := r.write(override, effective); err != nil {
			if rerr := r.restoreFiles(name); rerr != nil {
				log.Printf("traefikconfig: restore %s: %v", name, rerr)
			}
			r.finish(&run, StateRefused, "could not write the new files: "+err.Error())
			return
		}
	}
	r.restartAndWatch(ctx, &run)
}

// restartAndWatch restarts Traefik and watches it; on failure it rolls back.
func (r *Runner) restartAndWatch(ctx context.Context, run *Run) {
	r.step(run, StateRestarting)
	if err := r.restart(ctx, run); err != nil {
		r.rollback(ctx, run, "could not restart Traefik: "+err.Error())
		return
	}
	r.watchAndFinish(ctx, run)
}

func (r *Runner) watchAndFinish(ctx context.Context, run *Run) {
	r.step(run, StateChecking)
	err := r.watch(ctx, run)
	run.Log = r.logs(ctx, run)
	if err != nil {
		r.rollback(ctx, run, err.Error())
		return
	}
	r.healthy(ctx, run)
	r.finish(run, StateOK, "")
}

// restart records the restart count and the time, then restarts Traefik.
func (r *Runner) restart(ctx context.Context, run *Run) error {
	st, err := r.Traefik.State(ctx)
	if err != nil {
		return err
	}
	at := r.now().UTC()
	run.RestartCountBefore, run.RestartedAt = st.RestartCount, &at
	if err := r.save(*run); err != nil {
		log.Printf("traefikconfig: save run %s: %v", run.ID, err)
	}
	return r.Traefik.Restart(ctx)
}

// watch polls Traefik until it is healthy or WatchFor has passed. Exiting,
// restarting on its own or a `Command error` in its log fail at once; other
// errors in its log (an ACME rate limit, DNS not pointing here) do not.
func (r *Runner) watch(ctx context.Context, run *Run) error {
	watchFor := orDefault(r.WatchFor, 60*time.Second)
	every := orDefault(r.Every, 2*time.Second)
	start := r.now()
	var lastErr error
	for {
		st, err := r.Traefik.State(ctx)
		switch {
		case err != nil:
			lastErr = err
		case !st.Running:
			return errors.New("Traefik exited after the restart")
		case st.RestartCount > run.RestartCountBefore:
			return errors.New("Traefik keeps restarting")
		default:
			if logText := r.logs(ctx, run); strings.Contains(logText, "Command error") {
				return errors.New("Traefik rejected its config (Command error in its log)")
			}
			if r.Domain == "" {
				if r.now().Sub(start) >= 10*time.Second {
					return nil
				}
			} else if lastErr = r.Traefik.Healthz(ctx, st.IP, r.Domain); lastErr == nil {
				return nil
			}
		}
		if r.now().Sub(start) >= watchFor {
			msg := fmt.Sprintf("the panel did not answer through Traefik within %ds", int(watchFor.Seconds()))
			if lastErr != nil {
				msg += ": " + lastErr.Error()
			}
			return errors.New(msg)
		}
		if err := r.sleep(ctx, every); err != nil {
			return err
		}
	}
}

func (r *Runner) logs(ctx context.Context, run *Run) string {
	since := run.StartedAt
	if run.RestartedAt != nil {
		since = *run.RestartedAt
	}
	text, err := r.Traefik.LogsSince(ctx, since, logTail)
	if err != nil {
		return "could not read Traefik's log: " + err.Error()
	}
	return text
}

// healthy records the file Traefik now runs, healthy.
func (r *Runner) healthy(ctx context.Context, run *Run) {
	eff, err := os.ReadFile(r.Paths.Effective())
	if err == nil {
		run.EffectiveSHA256 = sha(eff)
		run.HealthySHA256 = run.EffectiveSHA256
	}
	if st, err := r.Traefik.State(ctx); err == nil && !st.StartedAt.IsZero() {
		started := st.StartedAt
		run.TraefikStartedAt = &started
	}
}

// rollback puts the backup back, restarts and watches again. It never loops:
// a failed rollback ends the run.
func (r *Runner) rollback(ctx context.Context, run *Run, reason string) {
	r.step(run, StateRollingBack)
	run.Reason = reason
	if run.Backup == "" {
		r.finish(run, StateRollbackFailed, reason+"; there is nothing to roll back to, because Traefik fails on its current files")
		return
	}
	if err := r.restoreFiles(run.Backup); err != nil {
		r.finish(run, StateRollbackFailed, reason+"; the backup could not be put back: "+err.Error())
		return
	}
	if err := r.restart(ctx, run); err != nil {
		r.finish(run, StateRollbackFailed, reason+"; Traefik could not be restarted on the backup: "+err.Error())
		return
	}
	err := r.watch(ctx, run)
	run.Log = r.logs(ctx, run)
	if err != nil {
		r.finish(run, StateRollbackFailed, reason+"; after the rollback: "+err.Error())
		return
	}
	r.healthy(ctx, run)
	r.finish(run, StateRolledBack, reason)
}

// dryRun starts the candidate in a throwaway Traefik. It returns a reason
// when it fails, with the dry-run's log.
func (r *Runner) dryRun(ctx context.Context, id string, effective []byte) (string, string) {
	st, err := r.Traefik.State(ctx)
	if err != nil {
		return "could not read Traefik's image for the dry-run: " + err.Error(), ""
	}
	dir := r.Paths.DryRunDir(id)
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "could not write the dry-run's config: " + err.Error(), ""
	}
	if err := WriteAtomic(dir+"/traefik.yml", effective); err != nil {
		return "could not write the dry-run's config: " + err.Error(), ""
	}
	res, err := r.Traefik.DryRun(ctx, id, dir, st.Image, orDefault(r.DryRunWait, 5*time.Second))
	switch {
	case err != nil:
		return "the dry-run could not run: " + err.Error(), res.Log
	case !res.Running:
		return "Traefik did not start on the new config in the dry-run", res.Log
	case strings.Contains(res.Log, "Command error"):
		return "Traefik rejected the new config in the dry-run", res.Log
	}
	return "", res.Log
}

// Test validates an override and dry-runs it; nothing is written.
func (r *Runner) Test(ctx context.Context, override []byte) (DryRunResult, []Problem, error) {
	effective, err := generateOrProblems(override)
	var pe *ProblemsError
	if errors.As(err, &pe) {
		return DryRunResult{}, pe.Problems, nil
	}
	reason, logText := r.dryRun(ctx, newID(), effective)
	if reason != "" {
		return DryRunResult{Running: false, Log: logText}, []Problem{{Message: reason}}, nil
	}
	return DryRunResult{Running: true, Log: logText}, nil, nil
}

// write writes the override, then the generated file.
func (r *Runner) write(override, effective []byte) error {
	if err := WriteAtomic(r.Paths.Override(), override); err != nil {
		return err
	}
	return WriteAtomic(r.Paths.Effective(), effective)
}

// Resume finishes a run a panel restart cut short, and removes dry-run
// Containers it left. Before the write, nothing changed: the run is refused.
func (r *Runner) Resume(ctx context.Context) error {
	if err := r.Traefik.RemoveDryRuns(ctx); err != nil {
		log.Printf("traefikconfig: remove stale dry-runs: %v", err)
	}
	run, err := r.Status()
	if err != nil || Terminal(run.State) {
		return err
	}
	r.mu.Lock()
	r.busy, r.done = true, make(chan struct{})
	r.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	switch run.State {
	case StateValidating, StateDryRun:
		go r.finish(&run, StateRefused, "the panel restarted before the new config was written")
	case StateWriting, StateRestarting:
		go r.restartAndWatch(ctx, &run)
	case StateChecking:
		go r.watchAndFinish(ctx, &run)
	default: // rolling-back
		go r.rollback(ctx, &run, run.Reason)
	}
	return nil
}

// PendingRestart reports whether traefik.yml differs from the file Traefik
// last started from, healthy; with no record, whether it is newer than
// Traefik's start.
func (r *Runner) PendingRestart(ctx context.Context) (bool, error) {
	eff, err := os.ReadFile(r.Paths.Effective())
	if err != nil {
		return false, nil
	}
	run, err := r.Status()
	if err != nil {
		return false, err
	}
	if run.HealthySHA256 != "" {
		return sha(eff) != run.HealthySHA256, nil
	}
	info, err := os.Stat(r.Paths.Effective())
	if err != nil {
		return false, nil
	}
	st, err := r.Traefik.State(ctx)
	if err != nil {
		return false, err
	}
	return info.ModTime().After(st.StartedAt), nil
}
