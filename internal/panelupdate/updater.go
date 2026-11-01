package panelupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Defaults of the health wait (spec "Apply" step 4.3).
const (
	DefaultHealthTimeout = 180 * time.Second
	DefaultHealthEvery   = 2 * time.Second
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
	ComposeUp(ctx context.Context, p Plan) error
	// ComposeStop stops one Service's Containers; one that is not there is
	// stopped.
	ComposeStop(ctx context.Context, p Plan, service string) error
	// Containers lists the project's Containers of a Service, found by the
	// Compose labels.
	Containers(ctx context.Context, project, service string) ([]ContainerInfo, error)
	// Healthz is GET https://<ip>/healthz with Host and SNI domain, the
	// certificate not verified; nil means 200.
	Healthz(ctx context.Context, ip, domain string) error
}

// Updater is the process `relist-panel panel-update apply --run <id>` runs.
type Updater struct {
	DataDir string
	Host    UpdaterHost
	Now     func() time.Time
	// HealthTimeout and HealthEvery default to 180 s and 2 s.
	HealthTimeout time.Duration
	HealthEvery   time.Duration
	// Sleep waits d or until ctx is done. Nil means a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Logf narrates the run; nil is silent.
	Logf func(format string, args ...any)
}

// ErrNotStarting means the run is not waiting for its Updater: it already ran,
// or never got this far. The Updater then does nothing.
var ErrNotStarting = errors.New("the run is not waiting for its Updater")

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

// Apply is steps 4.1 to 4.5 of the spec's Apply. It returns the run's last
// state, which is terminal, or an error when the run could not even be read or
// is not waiting; in the second case it has written nothing. status.json is
// written before each step and when the run ends.
func (u *Updater) Apply(ctx context.Context, run string) (RunStatus, error) {
	dir := RunDir(u.DataDir, run)
	st, err := ReadStatus(dir)
	if err != nil {
		return RunStatus{}, fmt.Errorf("read the run's status: %w", err)
	}
	if st.State != StateStartingUpdater {
		return st, fmt.Errorf("%w: it is %s", ErrNotStarting, st.State)
	}
	plan, err := ReadPlan(dir)
	if err != nil {
		return u.finish(dir, st, StateRolledBack, fmt.Sprintf("read plan.json: %v; nothing was changed", err), ""), nil
	}
	u.logf("update %s -> %s (run %s)", st.FromVersion, st.ToVersion, run)
	cause := u.update(ctx, dir, &st, plan)
	if cause == nil {
		u.logf("succeeded")
		return u.finish(dir, st, StateSucceeded, "", ""), nil
	}
	u.logf("failed: %v; rolling back", cause)
	return u.rollback(ctx, dir, st, plan, cause), nil
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

func (u *Updater) update(ctx context.Context, dir string, st *RunStatus, plan Plan) error {
	if err := u.step(dir, st, StateWritingFiles); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	install, err := os.ReadFile(filepath.Join(dir, candidateInstall))
	if err != nil {
		return fmt.Errorf("read the new Install file: %w", err)
	}
	env, err := os.ReadFile(filepath.Join(dir, candidateEnvFile))
	if err != nil {
		return fmt.Errorf("read the new .env: %w", err)
	}
	if err := replaceFile(plan.ConfigFiles[0], install, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", plan.ConfigFiles[0], err)
	}
	if err := replaceFile(plan.EnvFile(), env, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", plan.EnvFile(), err)
	}
	if err := u.step(dir, st, StateComposeUp); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	if err := u.Host.ComposeUp(ctx, plan); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	if err := u.step(dir, st, StateWaitingHealth); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return u.waitHealthy(ctx, plan, plan.TargetImageID)
}

// rollback puts the Install files, .env and panel.db back and brings the old
// panel up (spec 4.5). The update's own error is kept next to a rollback error.
func (u *Updater) rollback(ctx context.Context, dir string, st RunStatus, plan Plan, cause error) RunStatus {
	st.Error = cause.Error()
	failed := func(err error) RunStatus {
		u.logf("rollback failed: %v", err)
		return u.finish(dir, st, StateRollbackFailed, cause.Error(), err.Error())
	}
	if err := u.step(dir, &st, StateRollingBack); err != nil {
		return failed(fmt.Errorf("write status: %w", err))
	}
	if err := u.restoreFiles(dir); err != nil {
		return failed(fmt.Errorf("restore the Install files: %w", err))
	}
	if err := u.Host.ComposeStop(ctx, plan, panelServiceName); err != nil {
		return failed(fmt.Errorf("stop the panel: %w", err))
	}
	if err := restoreDatabase(u.DataDir, dir); err != nil {
		return failed(fmt.Errorf("restore panel.db: %w", err))
	}
	if err := u.Host.ComposeUp(ctx, plan); err != nil {
		return failed(fmt.Errorf("docker compose up: %w", err))
	}
	if err := u.waitHealthy(ctx, plan, plan.FromImageID); err != nil {
		return failed(fmt.Errorf("the old version: %w", err))
	}
	return u.finish(dir, st, StateRolledBack, cause.Error(), "")
}

// restoreFiles copies every backed-up Install file back to where it was, and
// takes away a .env the update created.
func (u *Updater) restoreFiles(dir string) error {
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
	plan, err := ReadPlan(dir)
	if err != nil {
		return err
	}
	if b.Env == nil {
		if err := os.Remove(plan.EnvFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
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

// restoreDatabase copies the run's backup over panel.db and removes the -wal
// and -shm files of the database it replaces. It only copies files: the
// Updater never opens the database.
func restoreDatabase(dataDir, runDir string) error {
	db := filepath.Join(dataDir, "panel.db")
	if err := copyFileAtomic(filepath.Join(runDir, backupDB), db, 0o600); err != nil {
		return err
	}
	for _, sibling := range []string{db + "-wal", db + "-shm"} {
		if err := os.Remove(sibling); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// waitHealthy polls until the Service panel runs the wanted image and the
// panel answers /healthz through Traefik, or the timeout passes. Traefik is
// looked up on every poll: a recreated Traefik has a new address.
func (u *Updater) waitHealthy(ctx context.Context, plan Plan, image string) error {
	ctx, cancel := context.WithTimeout(ctx, u.timeout())
	defer cancel()
	for {
		last := u.healthyOnce(ctx, plan, image)
		if last == nil {
			return nil
		}
		u.logf("not healthy yet: %v", last)
		if err := u.sleep(ctx, u.every()); err != nil {
			return fmt.Errorf("the panel was not healthy through Traefik within %s: %w", u.timeout(), last)
		}
	}
}

func (u *Updater) healthyOnce(ctx context.Context, plan Plan, image string) error {
	panels, err := u.Host.Containers(ctx, plan.Project, panelServiceName)
	if err != nil {
		return fmt.Errorf("list the panel Containers: %w", err)
	}
	runs := false
	for _, c := range panels {
		if c.Running && c.ImageID == image {
			runs = true
		}
	}
	if !runs {
		return fmt.Errorf("no running panel Container uses image %s", image)
	}
	traefiks, err := u.Host.Containers(ctx, plan.Project, traefikServiceKey)
	if err != nil {
		return fmt.Errorf("list the Traefik Containers: %w", err)
	}
	var last error = errors.New("no running Traefik Container is on " + networkName)
	for _, c := range traefiks {
		ip := c.IPs[networkName]
		if !c.Running || ip == "" {
			continue
		}
		if last = u.Host.Healthz(ctx, ip, plan.Domain); last == nil {
			return nil
		}
	}
	return last
}
