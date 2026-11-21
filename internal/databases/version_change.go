package databases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoPreviousData is a change that kept no data, or whose data is deleted.
var ErrNoPreviousData = errors.New("this version change has no previous version data")

// ErrNotHigher refuses a Change version to the same or a lower version:
// downgrades are never offered (ADR-0047).
var ErrNotHigher = errors.New("pick a version higher than the current one")

// ChangeVersion moves Database id to a higher version of its engine as a
// job, and returns the running record at once. Postgres is dumped and
// restored onto a new volume and rolled back on any failure; MariaDB and
// Redis change the image in place. backupFirst matters only for MariaDB:
// Postgres always backs up, Redis has no Backups.
func (s *Service) ChangeVersion(ctx context.Context, id int64, version string, backupFirst bool) (VersionChange, error) {
	if err := s.gate(ctx); err != nil {
		return VersionChange{}, err
	}
	var dp Dumper
	d, err := s.begin(ctx, id, JobKindChangeVersion, func(d Database) error {
		if d.PendingPassword != "" {
			return ErrPendingPassword
		}
		if _, err := Lookup(d.Engine, version); err != nil {
			return err
		}
		if compareTags(version, d.Version) <= 0 {
			return ErrNotHigher
		}
		if d.Engine == "redis" {
			return nil
		}
		if s.d.DataDir == "" {
			return errors.New("backups are not configured")
		}
		var err error
		dp, err = DumperFor(d.Engine)
		return err
	})
	if err != nil {
		return VersionChange{}, err
	}
	method := VersionMethodInPlace
	if d.Engine == "postgres" {
		method = VersionMethodDumpRestore
	}
	c, err := s.d.Store.InsertVersionChange(ctx, VersionChange{DatabaseID: id, FromVersion: d.Version, ToVersion: version, Method: method})
	if err != nil {
		s.endJob(id)
		return VersionChange{}, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		ctx := context.WithoutCancel(ctx)
		run := &versionRun{s: s, d: d, dp: dp, c: c}
		if method == VersionMethodDumpRestore {
			run.dumpRestore(ctx)
		} else {
			run.inPlace(ctx, backupFirst)
		}
	}()
	return c, nil
}

// withLock runs fn under Database id's lock: each host change of a version
// change takes it, but never a dump or a restore, so other actions are
// refused with ErrBusy by the job slot instead of waiting.
func (s *Service) withLock(id int64, fn func() error) error {
	unlock := s.lock(id)
	defer unlock()
	return fn()
}

// versionRun is one running version change and its step log.
type versionRun struct {
	s   *Service
	d   Database
	dp  Dumper
	c   VersionChange
	log strings.Builder
}

func (r *versionRun) logText() string {
	out := r.log.String()
	if len(out) > logLimit {
		out = out[len(out)-logLimit:]
	}
	return strings.ToValidUTF8(out, "")
}

func (r *versionRun) note(ctx context.Context, line string) {
	r.log.WriteString(line + "\n")
	_ = r.s.d.Store.SetVersionChangeLog(ctx, r.c.ID, r.logText())
}

// step runs fn as one named line of the log. On failure the log gets the
// cause and detail (a stderr tail), and the error is "<name>: <cause>".
// Both go through redactPasswords.
func (r *versionRun) step(ctx context.Context, name string, fn func() (detail string, err error)) error {
	r.note(ctx, "→ "+name)
	detail, err := fn()
	if err == nil {
		return nil
	}
	r.log.WriteString("✗ " + name + ": " + redactPasswords(err.Error()) + "\n")
	if detail = strings.TrimSpace(redactPasswords(detail)); detail != "" {
		r.log.WriteString(detail + "\n")
	}
	_ = r.s.d.Store.SetVersionChangeLog(ctx, r.c.ID, r.logText())
	return fmt.Errorf("%s: %w", name, err)
}

// fail ends the change as failed. A rollback that failed too is also the
// Database's last error: it may be down.
func (r *versionRun) fail(ctx context.Context, stepErr, rollbackErr error) {
	msg := redactPasswords(stepErr.Error())
	if rollbackErr != nil {
		msg += "; the rollback failed too: " + redactPasswords(rollbackErr.Error())
		r.note(ctx, "✗ Rollback: "+redactPasswords(rollbackErr.Error()))
		_ = r.s.d.Store.SetLastError(ctx, r.d.ID, msg)
	}
	_ = r.s.d.Store.FailVersionChange(ctx, r.c.ID, msg, r.logText())
}

// dumpRestore is the Postgres flow: pull, cut the clients off, back up every
// database with the roles and the per-database settings, stop, start the new
// version on a new volume, restore, switch. Any failure from the disconnect
// on puts the old server back as it was.
func (r *versionRun) dumpRestore(ctx context.Context) {
	s, d := r.s, r.d
	name := ContainerName(d.ID)
	oldSpec, err := appliedSpec(d)
	if err != nil {
		r.fail(ctx, err, nil)
		return
	}
	next := d
	next.Version, next.Volume = r.c.ToVersion, targetVolume(d.ID, r.c.ID)
	newSpec, _, err := specFor(next)
	if err != nil {
		r.fail(ctx, err, nil)
		return
	}
	if err := r.step(ctx, "Pull "+newSpec.Image, func() (string, error) {
		_, err := s.d.Host.PullImage(ctx, newSpec.Image)
		return "", err
	}); err != nil {
		r.fail(ctx, err, nil)
		return
	}

	stopped, created := false, ""
	rollback := func(stepErr error) {
		var rbErr error
		if !stopped {
			rbErr = s.withLock(d.ID, func() error { return s.d.Host.Realias(ctx, name, oldSpec.Alias) })
			if rbErr == nil {
				r.note(ctx, "Clients reconnected to "+oldSpec.Image)
			}
		} else if rbErr = r.restoreOld(ctx, oldSpec, created); rbErr == nil {
			r.note(ctx, fmt.Sprintf("Rolled back to %s on %s", oldSpec.Image, oldSpec.Volume))
		}
		r.fail(ctx, stepErr, rbErr)
	}

	if err := r.step(ctx, "Disconnect clients", func() (string, error) {
		return "", s.withLock(d.ID, func() error { return s.d.Host.Disconnect(ctx, name) })
	}); err != nil {
		rollback(err)
		return
	}
	var names []string
	if err := r.step(ctx, "List the databases", func() (string, error) {
		names, err = s.listLogical(ctx, d, r.dp)
		return "", err
	}); err != nil {
		rollback(err)
		return
	}
	files := map[string]string{}
	for _, n := range names {
		if err := r.step(ctx, "Back up "+n, func() (string, error) {
			b := s.backupNow(ctx, d, r.dp, n, TriggerPreUpdate, nil)
			if b.Status != JobSucceeded {
				return b.Log, errors.New(b.Error)
			}
			files[n] = filepath.Join(s.dbDir(d.ID), "backups", b.FileName)
			return "", nil
		}); err != nil {
			rollback(err)
			return
		}
	}
	rolesPath := filepath.Join(s.dbDir(d.ID), "backups", fmt.Sprintf("version-change-%d-roles.sql", r.c.ID))
	if err := r.step(ctx, "Back up the roles", func() (string, error) { return r.dumpRoles(ctx, rolesPath) }); err != nil {
		rollback(err)
		return
	}
	states := map[string]dbState{}
	for _, n := range names {
		if err := r.step(ctx, "Read the settings of "+n, func() (string, error) {
			st, log, err := s.captureState(ctx, d, r.dp, n)
			states[n] = st
			return log, err
		}); err != nil {
			rollback(err)
			return
		}
	}

	stopped = true
	if err := r.step(ctx, "Stop "+oldSpec.Image, func() (string, error) {
		return "", s.withLock(d.ID, func() error { return s.d.Host.Stop(ctx, name) })
	}); err != nil {
		rollback(err)
		return
	}
	if err := r.step(ctx, fmt.Sprintf("Start %s on %s", newSpec.Image, newSpec.Volume), func() (string, error) {
		if err := s.d.Store.SetVersionChangeTarget(ctx, r.c.ID, newSpec.Volume); err != nil {
			return "", err
		}
		created = newSpec.Volume
		// Create attaches relist-network; the new server stays off it until
		// the switch, or apps would write into it during the restore.
		if err := s.withLock(d.ID, func() error {
			if err := s.d.Host.Create(ctx, newSpec); err != nil {
				return err
			}
			return s.d.Host.Disconnect(ctx, name)
		}); err != nil {
			return "", err
		}
		return "", s.waitHealthy(ctx, name)
	}); err != nil {
		rollback(err)
		return
	}
	if err := r.step(ctx, "Restore the roles", func() (string, error) {
		raw, err := os.ReadFile(rolesPath)
		if err != nil {
			return "", err
		}
		_, log, err := s.admin(ctx, d, r.dp, filterRoles(string(raw)))
		return log, err
	}); err != nil {
		rollback(err)
		return
	}
	for _, n := range names {
		if err := r.step(ctx, "Restore "+n, func() (string, error) {
			return s.restoreInto(ctx, d, r.dp, states[n], files[n])
		}); err != nil {
			rollback(err)
			return
		}
	}
	if err := r.step(ctx, "Switch to "+newSpec.Image, func() (string, error) {
		raw, err := json.Marshal(newSpec)
		if err != nil {
			return "", err
		}
		return "", s.withLock(d.ID, func() error {
			return s.d.Store.CommitVersionChange(ctx, VersionCommit{ChangeID: r.c.ID, DatabaseID: d.ID, Version: r.c.ToVersion,
				Volume: newSpec.Volume, AppliedSpec: string(raw), FromVolume: oldSpec.Volume, Log: r.logText()})
		})
	}); err != nil {
		rollback(err)
		return
	}
	// Switched: the new volume is live, so a failure here must neither roll
	// back nor fail the row. Reconcile reconnects a container that is off
	// the network on the next start.
	if err := s.withLock(d.ID, func() error { return s.d.Host.Realias(ctx, name, newSpec.Alias) }); err != nil {
		msg := "reconnect the clients after the version change: " + err.Error()
		r.note(ctx, "✗ "+msg)
		_ = s.d.Store.SetLastError(ctx, d.ID, msg)
		return
	}
	r.note(ctx, "Clients reconnected to "+newSpec.Image)
}

// dumpRoles writes pg_dumpall --roles-only to path, readable by the panel
// only: it holds the roles' password hashes.
func (r *versionRun) dumpRoles(ctx context.Context, path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	errOut := &tailBuffer{max: logLimit}
	code, err := r.s.d.Host.Exec(ctx, ContainerName(r.d.ID), ExecRequest{
		Cmd: []string{"pg_dumpall", "-U", "postgres", "--roles-only"}, Env: r.dp.Env(r.d.Password), Stdout: f, Stderr: errOut})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && code != 0 {
		err = fmt.Errorf("pg_dumpall exited with code %d", code)
	}
	if err != nil {
		_ = os.Remove(path)
		return errOut.String(), err
	}
	return "", nil
}

// restoreOld removes the new container and its volume and starts the old
// container from its applied settings on its own volume.
func (r *versionRun) restoreOld(ctx context.Context, old ContainerSpec, created string) error {
	var errs []error
	err := r.s.withLock(r.d.ID, func() error {
		if err := r.s.d.Host.Remove(ctx, old.Name); err != nil {
			errs = append(errs, err)
		}
		if created != "" {
			if err := r.s.d.Host.RemoveVolume(ctx, created); err != nil {
				errs = append(errs, err)
			}
		}
		return r.s.d.Host.Create(ctx, old)
	})
	if err != nil {
		errs = append(errs, err)
	} else if err := r.s.waitHealthy(ctx, old.Name); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// inPlace is the MariaDB and Redis flow: back up (MariaDB, when asked),
// pull, switch the record, then start the new image on the same volume.
// Nothing is changed before the switch. After it there is no rollback: the
// new version may already have rewritten the data, which the old one cannot
// read (Redis 7.4 refuses an RDB that Redis 8 saved).
func (r *versionRun) inPlace(ctx context.Context, backupFirst bool) {
	s, d := r.s, r.d
	next := d
	next.Version = r.c.ToVersion
	newSpec, _, err := specFor(next)
	if err != nil {
		r.fail(ctx, err, nil)
		return
	}
	var backups []string
	if backupFirst && r.dp != nil {
		var names []string
		if err := r.step(ctx, "List the databases", func() (string, error) {
			names, err = s.listLogical(ctx, d, r.dp)
			return "", err
		}); err != nil {
			r.fail(ctx, err, nil)
			return
		}
		for _, n := range names {
			if err := r.step(ctx, "Back up "+n, func() (string, error) {
				b := s.backupNow(ctx, d, r.dp, n, TriggerPreUpdate, nil)
				if b.Status != JobSucceeded {
					return b.Log, errors.New(b.Error)
				}
				backups = append(backups, fmt.Sprintf("#%d", b.ID))
				return "", nil
			}); err != nil {
				r.fail(ctx, err, nil)
				return
			}
		}
	}
	if err := r.step(ctx, "Pull "+newSpec.Image, func() (string, error) {
		_, err := s.d.Host.PullImage(ctx, newSpec.Image)
		return "", err
	}); err != nil {
		r.fail(ctx, err, nil)
		return
	}
	if err := r.step(ctx, "Switch to "+newSpec.Image, func() (string, error) {
		raw, err := json.Marshal(newSpec)
		if err != nil {
			return "", err
		}
		return "", s.withLock(d.ID, func() error {
			return s.d.Store.CommitVersionChange(ctx, VersionCommit{ChangeID: r.c.ID, DatabaseID: d.ID, Version: r.c.ToVersion,
				Volume: volumeOf(d), AppliedSpec: string(raw), Log: r.logText(), KeepRunning: true})
		})
	}); err != nil {
		r.fail(ctx, err, nil)
		return
	}
	r.finishInPlace(ctx, backups)
}

// finishInPlace starts the new image after the switch: MariaDB once with
// MARIADB_AUTO_UPGRADE, then without it. A failure is not rolled back; the
// Database's last error says whether the new version ran at all and names
// the pre-update Backups. A restart in the middle runs it again from
// Startup.
func (r *versionRun) finishInPlace(ctx context.Context, backups []string) {
	s := r.s
	name := ContainerName(r.d.ID)
	next := r.d
	next.Version = r.c.ToVersion
	newSpec, _, err := specFor(next)
	if err != nil {
		r.fail(ctx, err, nil)
		return
	}
	// ran is whether the new image got as far as running on the data.
	ran := false
	start := func(spec ContainerSpec) error {
		if err := s.withLock(r.d.ID, func() error { return s.d.Host.Create(ctx, spec) }); err != nil {
			return err
		}
		ran = true
		return s.waitHealthy(ctx, name)
	}
	err = r.step(ctx, "Start "+newSpec.Image, func() (string, error) {
		if r.d.Engine != "mariadb" {
			return "", start(newSpec)
		}
		// MARIADB_AUTO_UPGRADE only for this first start: kept, it would be
		// in the applied settings for good. The label hash is this
		// container's own, so a restart in between recreates the plain one.
		upgrade := newSpec
		upgrade.Env = append(append([]string(nil), newSpec.Env...), "MARIADB_AUTO_UPGRADE=1")
		upgrade.Labels = map[string]string{IDLabel: newSpec.Labels[IDLabel], HashLabel: hashSpec(upgrade)}
		return "", start(upgrade)
	})
	if err == nil && r.d.Engine == "mariadb" {
		err = r.step(ctx, "Recreate without MARIADB_AUTO_UPGRADE", func() (string, error) { return "", start(newSpec) })
	}
	if err != nil {
		label, to, from := Engines[r.d.Engine].Label(), r.c.ToVersion, r.c.FromVersion
		var msg string
		if ran {
			msg = fmt.Sprintf("%s %s did not start: %s. It is not rolled back: %s may have rewritten the data, which %s cannot read.",
				label, to, redactPasswords(err.Error()), to, from)
		} else {
			msg = fmt.Sprintf("%s %s did not start: %s. It is not rolled back, but %s never ran, so the data is as %s left it; Start the Database to try %s again.",
				label, to, redactPasswords(err.Error()), to, from, to)
		}
		if len(backups) > 0 {
			msg += fmt.Sprintf(" The pre-update Backups %s hold the data from before the change.", strings.Join(backups, ", "))
		}
		_ = s.d.Store.SetLastError(ctx, r.d.ID, msg)
		r.fail(ctx, err, nil)
		return
	}
	r.log.WriteString("Done\n")
	_ = s.d.Store.SucceedVersionChange(ctx, r.c.ID, r.logText())
}

// resumeInterrupted finishes the in-place changes a restart cut off after
// their switch: the record already names the new version, so ReconcileAll
// alone would start it, but MariaDB without mariadb-upgrade. Every other
// running change is failed by the caller, and ReconcileAll puts its old
// container back.
func (s *Service) resumeInterrupted(ctx context.Context) error {
	running, err := s.d.Store.RunningVersionChanges(ctx)
	if err != nil {
		return err
	}
	for _, c := range running {
		d, err := s.d.Store.Get(ctx, c.DatabaseID)
		if err != nil || d.DeletedAt != nil || c.Method != VersionMethodInPlace || d.Version != c.ToVersion {
			continue
		}
		if err := s.beginJob(d.ID, JobKindChangeVersion); err != nil {
			continue
		}
		r := &versionRun{s: s, d: d, c: c}
		r.log.WriteString(c.Log)
		r.note(ctx, "Resumed after a restart")
		r.finishInPlace(ctx, s.preUpdateSince(ctx, d.ID, c.StartedAt))
		s.endJob(d.ID)
	}
	return nil
}

// preUpdateSince names Database id's succeeded pre-update Backups taken
// since a change started, as "#<id>".
func (s *Service) preUpdateSince(ctx context.Context, id int64, since time.Time) []string {
	all, err := s.d.Store.ListBackups(ctx, id)
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range all {
		if b.Trigger == TriggerPreUpdate && b.Status == JobSucceeded && !b.StartedAt.Before(since) {
			out = append(out, fmt.Sprintf("#%d", b.ID))
		}
	}
	return out
}

// ListVersionChanges lists Database id's version changes, newest first.
func (s *Service) ListVersionChanges(ctx context.Context, id int64) ([]VersionChange, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.DeletedAt != nil {
		return nil, ErrNotFound
	}
	return s.d.Store.ListVersionChanges(ctx, id)
}

// DeletePreviousData removes the volume a succeeded change left behind.
func (s *Service) DeletePreviousData(ctx context.Context, id, changeID int64) error {
	return s.opIdle(ctx, id, func(d Database) error {
		c, err := s.d.Store.GetVersionChange(ctx, id, changeID)
		if errors.Is(err, ErrNotFound) {
			return ErrNoPreviousData
		}
		if err != nil {
			return err
		}
		if c.Status != JobSucceeded || c.FromVolume == "" || c.FromVolumeRemovedAt != nil || c.FromVolume == volumeOf(d) {
			return ErrNoPreviousData
		}
		if err := s.d.Host.RemoveVolume(ctx, c.FromVolume); err != nil {
			return err
		}
		return s.d.Store.MarkFromVolumeRemoved(ctx, changeID)
	})
}

// SweepFailedVersionVolumes removes the volumes failed or interrupted changes
// were creating. Each name is unique to its change, so none is live data. A
// volume still in use is logged and left for the next start.
func (s *Service) SweepFailedVersionVolumes(ctx context.Context) error {
	vols, err := s.d.Store.FailedTargetVolumes(ctx)
	if err != nil {
		return err
	}
	for _, v := range vols {
		if err := s.d.Host.RemoveVolume(ctx, v); err != nil {
			log.Printf("databases: remove the volume %s of a failed version change: %v", v, err)
		}
	}
	return nil
}
