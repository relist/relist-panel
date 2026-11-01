package panelupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Defaults of the health wait (spec "Apply" step 8).
const (
	DefaultHealthTimeout = 180 * time.Second
	DefaultHealthEvery   = 2 * time.Second
	// DefaultUpTimeout bounds one `docker compose up` (a pull can hang).
	DefaultUpTimeout = 15 * time.Minute
)

// networkName is the shared network Traefik is reached on.
const networkName = "relist-network"

// Services of the Install the Updater looks for by Compose label.
const (
	panelServiceName  = "panel"
	traefikServiceKey = "traefik"
)

// ContainerInfo is a Container of the Install's project, as the Updater needs
// to see it.
type ContainerInfo struct {
	ID      string
	ImageID string
	Running bool
	// IPs maps a network name to the Container's address on it.
	IPs map[string]string
}

// UpdaterHost is what the Updater does to the host: `docker compose`, the
// Engine API and the health request. dockerexec implements it.
type UpdaterHost interface {
	// ComposeUp runs `docker compose -p … --project-directory … -f … up -d`
	// with the Install's own files, and .env from the Install directory.
	ComposeUp(ctx context.Context, in Install) error
	// ComposeStop stops one Service's Containers; one that is not there is
	// stopped.
	ComposeStop(ctx context.Context, in Install, service string) error
	// Containers lists the project's Containers of a Service, found by the
	// Compose labels.
	Containers(ctx context.Context, project, service string) ([]ContainerInfo, error)
	// Healthz is GET https://<ip>/healthz with Host and SNI domain, the
	// certificate not verified; nil means 200.
	Healthz(ctx context.Context, ip, domain string) error
	// UpdaterState is what the Engine says of an Updater Container by name.
	UpdaterState(ctx context.Context, name string) (UpdaterState, error)
}

// PreflightChecker is the re-check Apply makes; *Checker is the real one.
type PreflightChecker interface {
	Check(ctx context.Context, in Install) (*Report, *Candidate, error)
}

// Updater is the process `relist-panel update --report <id>` runs. It does the
// whole Panel update: re-check, quiesce, backup, write, up, health, rollback.
type Updater struct {
	DataDir string
	Host    UpdaterHost
	Checker PreflightChecker
	// Domain is RELIST_DOMAIN: the Host and SNI of the health request.
	Domain string
	Now    func() time.Time
	// HealthTimeout and HealthEvery default to 180 s and 2 s.
	HealthTimeout time.Duration
	HealthEvery   time.Duration
	// UpTimeout bounds each `docker compose up`; it defaults to 15 minutes.
	// A hung pull then fails the step and the run rolls back.
	UpTimeout time.Duration
	// AnswerWindow is how long the panel has to answer the quiesce request
	// (default 20 s); AckEvery is how often panel.json is read (default 2 s).
	AnswerWindow time.Duration
	AckEvery     time.Duration
	// Signals delivers SIGTERM and the like. One before the backup starts
	// ends the run as aborted; after that they are ignored.
	Signals <-chan os.Signal
	// Sleep waits d or until ctx is done. Nil means a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Logf narrates the run; nil is silent.
	Logf func(format string, args ...any)
}

// ApplyArgs is what `update --report` was given.
type ApplyArgs struct {
	Run, Report string
	StepsDone   []string
	In          Install
}

func (u *Updater) logf(format string, args ...any) {
	if u.Logf != nil {
		u.Logf(format, args...)
	}
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) timeout() time.Duration {
	if u.HealthTimeout > 0 {
		return u.HealthTimeout
	}
	return DefaultHealthTimeout
}

// composeUp runs `up` for at most UpTimeout.
func (u *Updater) composeUp(ctx context.Context, in Install) error {
	d := u.UpTimeout
	if d <= 0 {
		d = DefaultUpTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	if err := u.Host.ComposeUp(ctx, in); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("did not finish within %s: %w", d, err)
		}
		return err
	}
	return nil
}

func (u *Updater) every() time.Duration {
	if u.HealthEvery > 0 {
		return u.HealthEvery
	}
	return DefaultHealthEvery
}

func (u *Updater) sleep(ctx context.Context, d time.Duration) error {
	if u.Sleep != nil {
		return u.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ErrRunExists means the run directory is already there: a run id is used
// once.
var ErrRunExists = errors.New("the run directory already exists")

// Apply is the spec's Apply. It returns the run's last state, which is
// terminal, or an error when it refused before it wrote anything. Before
// quiescing nothing is written except one status.json in state aborted when
// the re-check fails. status.json is written before each step and when the run
// ends.
func (u *Updater) Apply(ctx context.Context, a ApplyArgs) (RunStatus, error) {
	dir := RunDir(u.DataDir, a.Run)
	if _, err := os.Lstat(dir); err == nil {
		return RunStatus{}, fmt.Errorf("%w: %s", ErrRunExists, dir)
	}
	// 1. Stale runs.
	if err := u.settleStale(ctx); err != nil {
		return RunStatus{}, err
	}
	// 2. Re-check.
	report, err := ReadReport(u.DataDir, a.Report)
	if err != nil {
		return u.abortBeforeStart(dir, a, nil, err.Error())
	}
	if missing := unticked(report.ManualSteps, a.StepsDone); len(missing) > 0 {
		return u.abortBeforeStart(dir, a, report, fmt.Sprintf("manual steps not confirmed: %s", strings.Join(missing, ", ")))
	}
	fresh, cand, err := u.Checker.Check(ctx, a.In)
	switch {
	case err != nil:
		return u.abortBeforeStart(dir, a, report, "the re-check could not run: "+err.Error())
	case fresh.Failed != nil:
		return u.abortBeforeStart(dir, a, report, "the re-check failed: "+fresh.Failed.Message)
	case fresh.NeedsManualUpdate:
		return u.abortBeforeStart(dir, a, report, "this version needs a manual update; see "+ReadmeURL)
	case fresh.UpToDate:
		return u.abortBeforeStart(dir, a, report, "the panel is already up to date")
	case !fresh.Ready:
		return u.abortBeforeStart(dir, a, report, "the Install does not meet this version's requirements")
	case fresh.Fingerprint != report.Fingerprint:
		return u.abortBeforeStart(dir, a, report, "the Install or the release changed since the preflight; run it again")
	}
	// 3. Quiesce.
	if err := os.MkdirAll(RunsDir(u.DataDir), 0o700); err != nil {
		return RunStatus{}, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return RunStatus{}, err
	}
	st := RunStatus{
		Run: a.Run, FromImage: u.fromImage(ctx, a.In), FromVersion: report.FromVersion, ToVersion: report.ToVersion,
		StartedAt: u.now().Unix(),
	}
	if err := u.step(dir, &st, StateQuiescing); err != nil {
		return RunStatus{}, fmt.Errorf("write status: %w", err)
	}
	outcome, detail, err := u.quiesce(ctx, dir, &st, a.In)
	if err != nil {
		return u.finish(dir, st, StateAborted, "the quiesce could not finish: "+err.Error(), ""), nil
	}
	switch outcome {
	case quiesceCancelled:
		return u.finish(dir, st, StateAborted, "cancelled", ""), nil
	case quiesceRefused:
		return u.finish(dir, st, StateAborted, detail, ""), nil
	case quiesceNoAnswer:
		if detail != "gone" {
			u.logf("stopping the panel Container")
			if err := u.Host.ComposeStop(ctx, a.In, panelServiceName); err != nil {
				return u.finish(dir, st, StateAborted, "stop the panel: "+err.Error(), ""), nil
			}
		}
		st.Note = PanelNoAnswerNote
	}
	if u.signalled() {
		return u.finish(dir, st, StateAborted, "cancelled", ""), nil
	}
	// 5. Backup. From here on a signal is ignored.
	if err := u.step(dir, &st, StateBackingUp); err != nil {
		return RunStatus{}, fmt.Errorf("write status: %w", err)
	}
	files, err := ReadLocalFiles(a.In)
	if err == nil {
		err = sameFiles(files, cand.Files)
	}
	if err != nil {
		return u.finish(dir, st, StateAborted, "the Install changed while the update waited: "+err.Error(), ""), nil
	}
	if err := writeBackup(ctx, u.DataDir, dir, a.In, files); err != nil {
		return u.finish(dir, st, StateAborted, "back up: "+err.Error(), ""), nil
	}
	pruneRuns(u.DataDir, a.Run)
	if err := refuseSymlinks(a.In); err != nil {
		u.logf("refusing: %v", err)
		return u.finish(dir, st, StateAborted, fmt.Sprintf("nothing was changed: %v", err), ""), nil
	}
	u.logf("update %s -> %s (run %s)", st.FromVersion, st.ToVersion, a.Run)
	cause := u.update(ctx, dir, &st, a.In, cand)
	if cause == nil {
		u.logf("succeeded")
		return u.finish(dir, st, StateSucceeded, "", ""), nil
	}
	u.logf("failed: %v; rolling back", cause)
	return u.rollback(ctx, dir, st, a.In, cause), nil
}

// abortBeforeStart records a re-check that refused the run: one status.json in
// state aborted, with the reason. Nothing else was written.
func (u *Updater) abortBeforeStart(dir string, a ApplyArgs, report *Report, reason string) (RunStatus, error) {
	u.logf("aborted: %s", reason)
	st := RunStatus{Run: a.Run, State: StateAborted, Error: reason, StartedAt: u.now().Unix()}
	if report != nil {
		st.FromVersion, st.ToVersion = report.FromVersion, report.ToVersion
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return st, err
	}
	if err := WriteStatus(dir, st, u.now()); err != nil {
		return st, err
	}
	return st, nil
}

// settleStale is Apply step 1: a newest run that is not over, whose Updater
// Container is gone or has exited, is marked interrupted; one whose Updater
// still runs refuses this run.
func (u *Updater) settleStale(ctx context.Context) error {
	st, ok, err := NewestRun(u.DataDir)
	if err != nil {
		return fmt.Errorf("read the newest run: %w", err)
	}
	if !ok || IsTerminal(st.State) {
		return nil
	}
	state, err := u.Host.UpdaterState(ctx, UpdaterName(st.Run))
	if err != nil {
		return fmt.Errorf("inspect the Updater of run %s: %w", st.Run, err)
	}
	if state.Running {
		return fmt.Errorf("the Updater %s is still running; wait for it, or stop it with docker stop %s", UpdaterName(st.Run), UpdaterName(st.Run))
	}
	st.State, st.Error = StateInterrupted, fmt.Sprintf("the Updater Container exited before the run was over (last state: %s)", st.State)
	u.logf("run %s was left %s: marking it interrupted", st.Run, st.State)
	return WriteStatus(RunDir(u.DataDir, st.Run), st, u.now())
}

// fromImage is the ID of the image the panel runs from now, which a rollback
// waits for; empty when the Install has no panel Container.
func (u *Updater) fromImage(ctx context.Context, in Install) string {
	panels, err := u.Host.Containers(ctx, in.Project, panelServiceName)
	if err != nil {
		return in.Image
	}
	for _, c := range panels {
		if c.Running {
			return c.ImageID
		}
	}
	if len(panels) > 0 {
		return panels[0].ImageID
	}
	return in.Image
}

// sameFiles fails when the Install's files are not the ones the re-check read.
func sameFiles(now, checked InstallFiles) error {
	if len(now.Config) != len(checked.Config) {
		return errors.New("the Install files changed")
	}
	for i := range now.Config {
		if !bytes.Equal(now.Config[i], checked.Config[i]) {
			return fmt.Errorf("config file %d changed after the preflight", i)
		}
	}
	if !bytes.Equal(now.Env, checked.Env) {
		return errors.New(".env changed after the preflight")
	}
	return nil
}

// refuseSymlinks fails when the Install file or .env is a symlink now: the
// rename that writes a file would replace the link, and the rollback could not
// put the operator's file back. The backup refuses them too; this is the
// check at write time.
func refuseSymlinks(in Install) error {
	for _, path := range []string{in.ConfigFiles[0], in.EnvFile()} {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; Panel update needs regular files in the Install directory", path)
		}
	}
	return nil
}

// step records the state a step is about to run in.
func (u *Updater) step(dir string, st *RunStatus, state string) error {
	st.State = state
	u.logf("%s", state)
	return WriteStatus(dir, *st, u.now())
}

// finish writes a terminal state. A failed write is logged: the watcher then
// finds the run not over and marks it interrupted.
func (u *Updater) finish(dir string, st RunStatus, state, errText, rollbackErr string) RunStatus {
	st.State, st.Error, st.RollbackError = state, errText, rollbackErr
	if err := WriteStatus(dir, st, u.now()); err != nil {
		u.logf("write the final state %s: %v", state, err)
	}
	return st
}

func (u *Updater) update(ctx context.Context, dir string, st *RunStatus, in Install, cand *Candidate) error {
	if err := u.step(dir, st, StateWritingFiles); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	if err := replaceFile(in.ConfigFiles[0], cand.Install, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", in.ConfigFiles[0], err)
	}
	if err := replaceFile(in.EnvFile(), cand.Env, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", in.EnvFile(), err)
	}
	if err := u.step(dir, st, StateComposeUp); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	if err := u.composeUp(ctx, in); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	if err := u.step(dir, st, StateWaitingHealth); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return u.waitHealthy(ctx, in, cand.ImageID)
}

// rollback puts the Install files, .env and panel.db back and brings the old
// panel up (spec 4.5). The update's own error is kept next to a rollback error.
func (u *Updater) rollback(ctx context.Context, dir string, st RunStatus, in Install, cause error) RunStatus {
	st.Error = cause.Error()
	failed := func(err error) RunStatus {
		u.logf("rollback failed: %v", err)
		return u.finish(dir, st, StateRollbackFailed, cause.Error(), err.Error())
	}
	if err := u.step(dir, &st, StateRollingBack); err != nil {
		return failed(fmt.Errorf("write status: %w", err))
	}
	if err := u.restoreFiles(dir, in); err != nil {
		return failed(fmt.Errorf("restore the Install files: %w", err))
	}
	if err := u.Host.ComposeStop(ctx, in, panelServiceName); err != nil {
		return failed(fmt.Errorf("stop the panel: %w", err))
	}
	if err := restoreDatabase(u.DataDir, dir); err != nil {
		return failed(fmt.Errorf("restore panel.db: %w", err))
	}
	if err := u.composeUp(ctx, in); err != nil {
		return failed(fmt.Errorf("docker compose up: %w", err))
	}
	if err := u.waitHealthy(ctx, in, st.FromImage); err != nil {
		return failed(fmt.Errorf("the old version: %w", err))
	}
	return u.finish(dir, st, StateRolledBack, cause.Error(), "")
}

// restoreFiles copies every backed-up Install file back to where it was, and
// takes away a .env the update created.
func (u *Updater) restoreFiles(dir string, in Install) error {
	b, err := ReadBackup(dir)
	if err != nil {
		return fmt.Errorf("read backup.json: %w", err)
	}
	for _, f := range b.Config {
		data, err := os.ReadFile(filepath.Join(dir, f.Copy))
		if err != nil {
			return err
		}
		if err := restoreFile(f.Path, data, 0o644); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	if b.Env == nil {
		if err := os.Remove(in.EnvFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, b.Env.Copy))
	if err != nil {
		return err
	}
	if err := restoreFile(b.Env.Path, data, 0o600); err != nil {
		return fmt.Errorf("%s: %w", b.Env.Path, err)
	}
	return nil
}

// restoreDatabase puts the run's backup in place of panel.db. The -wal and
// -shm files of the database it replaces go first, so a stale WAL can never
// sit next to the restored file; if one cannot be removed, panel.db is left
// as it was. It only copies files: the Updater never opens the database.
func restoreDatabase(dataDir, runDir string) error {
	db := filepath.Join(dataDir, "panel.db")
	for _, sibling := range []string{db + "-wal", db + "-shm"} {
		if err := os.Remove(sibling); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return copyFileAtomic(filepath.Join(runDir, backupDB), db, 0o600)
}

// waitHealthy polls until the Service panel runs the wanted image and the
// panel answers /healthz through Traefik, or the timeout passes. Traefik is
// looked up on every poll: a recreated Traefik has a new address.
func (u *Updater) waitHealthy(ctx context.Context, in Install, image string) error {
	ctx, cancel := context.WithTimeout(ctx, u.timeout())
	defer cancel()
	for {
		last := u.healthyOnce(ctx, in, image)
		if last == nil {
			return nil
		}
		u.logf("not healthy yet: %v", last)
		if err := u.sleep(ctx, u.every()); err != nil {
			return fmt.Errorf("the panel was not healthy through Traefik within %s: %w", u.timeout(), last)
		}
	}
}

func (u *Updater) healthyOnce(ctx context.Context, in Install, image string) error {
	panels, err := u.Host.Containers(ctx, in.Project, panelServiceName)
	if err != nil {
		return fmt.Errorf("list the panel Containers: %w", err)
	}
	runs := false
	for _, c := range panels {
		// An empty image (an Install that had no panel Container) is not checked.
		if c.Running && (image == "" || c.ImageID == image) {
			runs = true
		}
	}
	if !runs {
		return fmt.Errorf("no running panel Container uses image %s", image)
	}
	traefiks, err := u.Host.Containers(ctx, in.Project, traefikServiceKey)
	if err != nil {
		return fmt.Errorf("list the Traefik Containers: %w", err)
	}
	var last error = errors.New("no running Traefik Container is on " + networkName)
	for _, c := range traefiks {
		ip := c.IPs[networkName]
		if !c.Running || ip == "" {
			continue
		}
		if last = u.Host.Healthz(ctx, ip, u.Domain); last == nil {
			return nil
		}
	}
	return last
}
