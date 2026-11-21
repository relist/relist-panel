package databases

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"relist-panel/internal/dbquery"
)

// ErrVolumeNotMissing refuses a Recreate of a Database whose container or
// volume is still there: only a lost volume is recreated (ADR-0050).
var ErrVolumeNotMissing = errors.New("this Database's volume is not missing; Recreate is only for a Database whose volume is gone")

// ErrSameDatabaseTwice is a Recreate given two Backups of one database.
type ErrSameDatabaseTwice struct{ Name string }

func (e ErrSameDatabaseTwice) Error() string {
	return "two Backups of " + e.Name + " were picked; pick one"
}

// recreatePick is one Backup a Recreate restores: its checked file and the
// Restore row that records it.
type recreatePick struct {
	backup  Backup
	path    string
	restore Restore
}

// Recreate gives a Database whose volume is gone a new, empty volume and
// starts it with its password. It then puts back the users the panel made,
// with their passwords, restores each Backup into its own database as it
// was backed up, and gives each of those users the access the Backup
// recorded (ADR-0050). Every check runs before anything is created; the
// rest runs as a job, and what fails is kept on its Restore and in the last
// error, never rolled back.
func (s *Service) Recreate(ctx context.Context, id int64, confirm string, backupIDs []int64) error {
	if err := s.gate(ctx); err != nil {
		return err
	}
	if s.d.DataDir == "" {
		return errors.New("backups are not configured")
	}
	unlock := s.lock(id)
	defer unlock()
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.DeletedAt != nil {
		return ErrNotFound
	}
	dp, err := DumperFor(d.Engine)
	if err != nil {
		return err
	}
	if confirm != d.Name {
		return ErrConfirm
	}
	if s.Jobs(id) != "" || s.isInflight(id) {
		return ErrBusy
	}
	if gone, err := s.volumeGone(ctx, d); err != nil {
		return err
	} else if !gone {
		return ErrVolumeNotMissing
	}
	picks, err := s.recreatePicks(ctx, id, backupIDs)
	if err != nil {
		return err
	}
	if err := s.beginJob(id, JobKindRecreate); err != nil {
		return err
	}
	abort := func(err error) error {
		for _, p := range picks {
			if p.restore.ID != 0 {
				s.finishRestore(ctx, p.restore, nil, JobFailed, "Recreate did not start: "+err.Error(), "")
			}
		}
		s.endJob(id)
		return err
	}
	for i := range picks {
		bid := picks[i].backup.ID
		r, err := s.d.Store.InsertRestore(ctx, Restore{DatabaseID: id, Source: "backup", BackupID: &bid, Target: picks[i].backup.LogicalName})
		if err != nil {
			return abort(err)
		}
		picks[i].restore = r
	}
	if d.Desired != DesiredRunning {
		// A restore needs the server up, and Recreate means to run it.
		if err := s.d.Store.SetDesired(ctx, id, DesiredRunning); err != nil {
			return abort(err)
		}
		d.Desired = DesiredRunning
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		ctx := context.WithoutCancel(ctx)
		s.containRows(ctx, id, "recreate", func() { s.executeRecreate(ctx, d, dp, picks) })
	}()
	return nil
}

// volumeGone reports whether d's volume is missing: d was initialized, and
// both its container and its volume are gone.
func (s *Service) volumeGone(ctx context.Context, d Database) (bool, error) {
	spec, err := appliedSpec(d)
	if err != nil {
		return false, err
	}
	st, err := s.d.Host.Inspect(ctx, spec.Name)
	if err != nil {
		return false, err
	}
	return s.volumeGoneFor(ctx, d, spec, st)
}

// volumeGoneFor is volumeGone for a container already inspected.
func (s *Service) volumeGoneFor(ctx context.Context, d Database, spec ContainerSpec, st ContainerState) (bool, error) {
	if !d.Initialized || st.Exists {
		return false, nil
	}
	ok, err := s.d.Host.VolumeExists(ctx, spec.Volume)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// recreatePicks checks each Backup a Recreate restores, as a Restore
// would: succeeded, on disk unchanged, of one database each.
func (s *Service) recreatePicks(ctx context.Context, id int64, ids []int64) ([]recreatePick, error) {
	seen := map[string]bool{}
	picks := make([]recreatePick, 0, len(ids))
	for _, bid := range ids {
		path, b, err := s.BackupPath(ctx, id, bid)
		if err != nil {
			return nil, err
		}
		if b.LogicalName == "" {
			return nil, ErrNotRestorable
		}
		if err := checkName(b.LogicalName); err != nil {
			return nil, err
		}
		if seen[b.LogicalName] {
			return nil, ErrSameDatabaseTwice{Name: b.LogicalName}
		}
		seen[b.LogicalName] = true
		sum, err := fileSHA(path)
		if err != nil {
			return nil, err
		}
		if sum != b.SHA256 {
			return nil, ErrChangedFile
		}
		picks = append(picks, recreatePick{backup: b, path: path})
	}
	return picks, nil
}

// recreatingFile names the marker a Recreate keeps on disk while it runs.
const recreatingFile = "recreating"

// recreatingMarker is the file that says d's Recreate has not ended: if the
// panel stops during it, the next Startup finds it (ADR-0050).
func (s *Service) recreatingMarker(id int64) string {
	return filepath.Join(s.dbDir(id), recreatingFile)
}

// executeRecreate is Recreate's job. Until the restores start, a failure
// takes away the container and the volume it made, so the Database is
// volume_missing again and Recreate can run again on a new volume; a panic
// does the same at any point, as a stopped panel does at the next Startup.
func (s *Service) executeRecreate(ctx context.Context, d Database, dp Dumper, picks []recreatePick) {
	spec, err := appliedSpec(d)
	if err != nil {
		s.failRecreate(ctx, d.ID, spec, picks, err, false)
		return
	}
	ended := false
	defer func() {
		if !ended { // a panic: containRows records it after this
			s.failRecreate(ctx, d.ID, spec, picks, errors.New("the job stopped unexpectedly"), true)
		}
	}()
	marker := s.recreatingMarker(d.ID)
	err = os.MkdirAll(filepath.Dir(marker), 0o700)
	if err == nil {
		err = os.WriteFile(marker, nil, 0o600)
	}
	if err == nil {
		unlock := s.lock(d.ID)
		var now Database
		var nowSpec ContainerSpec
		if now, nowSpec, err = s.recreateSpec(ctx, d.ID); err == nil {
			d, spec = now, nowSpec
			err = s.create(ctx, d, spec)
		}
		unlock()
	}
	if err == nil {
		err = s.waitHealthy(ctx, spec.Name)
	}
	var managed recreatedUsers
	var t dbquery.Target
	var a accountSQL
	if err == nil {
		managed, t, a, err = s.recreateUsers(ctx, d)
	}
	if err != nil {
		ended = true
		s.failRecreate(ctx, d.ID, spec, picks, scrubErr(err, d.Password), true)
		return
	}

	var problems []string
	for _, name := range managed.failed {
		problems = append(problems, "user "+name+": "+managed.errs[name])
	}
	for _, p := range picks {
		target := p.backup.LogicalName
		log, err := s.recreateDatabase(ctx, d, dp, p)
		if err != nil {
			s.finishRestore(ctx, p.restore, nil, JobFailed, err.Error(), log)
			problems = append(problems, target+": "+err.Error())
			continue
		}
		s.finishRestore(ctx, p.restore, nil, JobSucceeded, "", log)
		if p.backup.State.Prev == "" {
			problems = append(problems, target+": "+statelessFix(d.Engine, target))
		}
		access := p.backup.State.Access
		users := make([]string, 0, len(access))
		for u := range access {
			users = append(users, u)
		}
		slices.Sort(users)
		for _, u := range users {
			if !managed.ok[u] {
				continue // only the users the panel made exist again
			}
			if err := s.setUserAccess(ctx, d, t, a, u, map[string]Access{target: access[u]}); err != nil {
				problems = append(problems, "the access of "+u+" to "+target+": "+err.Error())
			}
		}
	}
	ended = true
	// Removing the marker is what makes the Recreate final: one left behind
	// makes the next Startup remove this volume.
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		problems = append(problems, "remove "+marker+" by hand, or the next panel start removes this volume: "+err.Error())
	}
	msg := ""
	if len(problems) > 0 {
		msg = "Recreated, but: " + strings.Join(problems, "; ")
	}
	_ = s.d.Store.SetLastError(context.WithoutCancel(ctx), d.ID, msg)
}

// recreateSpec reads d again, under its lock, for the container: a Rename
// since Recreate's checks changes its alias. The applied settings' env gets
// d's password, which a reset the panel stopped in the middle of may not
// have written there: the new server is initialized with it, and every
// admin step uses it.
func (s *Service) recreateSpec(ctx context.Context, id int64) (Database, ContainerSpec, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return d, ContainerSpec{}, err
	}
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		return d, ContainerSpec{}, err
	}
	spec, err := appliedSpec(d)
	if err != nil {
		return d, spec, err
	}
	if env := swapEnv(spec.Env, eng.Env(d.Password)); !slices.Equal(env, spec.Env) {
		spec.Env = env
		if err := s.saveApplied(ctx, &d, spec); err != nil {
			return d, spec, err
		}
		spec, err = appliedSpec(d)
	}
	return d, spec, err
}

// statelessNote and statelessFix say what a Backup taken before Backups
// kept their database's state (migration 00055) brings back, and the fix.
func statelessNote(engine, target string) string {
	if engine == "postgres" {
		return "This Backup was taken before Backups kept their database's owner and access, so postgres owns " + target + " and no user can reach it."
	}
	return "This Backup was taken before Backups kept their users' access, so no user can reach " + target + "."
}

func statelessFix(engine, target string) string {
	if engine == "postgres" {
		return "its Backup was taken before Backups kept the owner and access, so postgres owns it and no user can reach it; make its owner with ALTER DATABASE " + pgIdent(target) + " OWNER TO \"<user>\" in the Query tab, then give access in the Users tab"
	}
	return "its Backup was taken before Backups kept users' access, so no user can reach it; give access in the Users tab"
}

// failRecreate ends a Recreate that failed before its restores: it takes
// away what the job made (made is false when it made nothing), fails its
// Restores and records why, so the Database is volume_missing again.
func (s *Service) failRecreate(ctx context.Context, id int64, spec ContainerSpec, picks []recreatePick, cause error, made bool) {
	ctx = context.WithoutCancel(ctx)
	msg := "Recreate failed: " + cause.Error()
	if made {
		if err := s.unmakeRecreate(ctx, id, spec); err != nil {
			msg += "; removing the container and volume it made failed too: " + err.Error()
		} else {
			msg += ". The volume it made was removed, so Recreate can run again."
		}
	}
	for _, p := range picks {
		if p.restore.ID != 0 {
			s.finishRestore(ctx, p.restore, nil, JobFailed, msg, "")
		}
	}
	_ = s.d.Store.SetLastError(ctx, id, msg)
}

// unmakeRecreate removes the container and the volume an unfinished
// Recreate made, then its marker. The volume is the job's own: it was gone
// when the Recreate started, and nothing else ran on it since. The marker
// stays when the host refuses, so the next Startup tries again.
func (s *Service) unmakeRecreate(ctx context.Context, id int64, spec ContainerSpec) error {
	unlock := s.lock(id)
	defer unlock()
	if err := s.d.Host.Remove(ctx, spec.Name); err != nil {
		return err
	}
	if err := s.d.Host.RemoveVolume(ctx, spec.Volume); err != nil {
		return err
	}
	if err := os.Remove(s.recreatingMarker(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// finishInterruptedRecreates is Startup's part for a panel that stopped
// during a Recreate: what the job made is removed before ReconcileAll could
// start it, and the Database is volume_missing again with the reason. When
// the host refuses, the Database is stopped instead of serving half its data.
func (s *Service) finishInterruptedRecreates(ctx context.Context) error {
	markers, err := filepath.Glob(filepath.Join(s.d.DataDir, ".databases", "*", recreatingFile))
	if err != nil {
		return err
	}
	var errs []error
	for _, marker := range markers {
		id, err := strconv.ParseInt(filepath.Base(filepath.Dir(marker)), 10, 64)
		if err != nil {
			continue
		}
		d, err := s.d.Store.Get(ctx, id)
		if errors.Is(err, ErrNotFound) || (err == nil && d.DeletedAt != nil) {
			errs = append(errs, removeIfThere(marker))
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		spec, err := appliedSpec(d)
		if err == nil {
			err = s.unmakeRecreate(ctx, id, spec)
		}
		if err != nil {
			errs = append(errs, err, s.d.Store.SetDesired(ctx, id, DesiredStopped))
			errs = append(errs, s.d.Store.SetLastError(ctx, id, "Recreate did not finish: the panel stopped during it, and removing the container and volume it made failed: "+err.Error()+". The Database is stopped."))
			continue
		}
		errs = append(errs, s.d.Store.SetLastError(ctx, id, "Recreate did not finish: the panel stopped during it, so the container and volume it made were removed. Recreate can run again."))
	}
	return errors.Join(errs...)
}

func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// recreatedUsers is how putting the managed users back went.
type recreatedUsers struct {
	ok     map[string]bool
	failed []string
	errs   map[string]string
}

// recreateUsers creates again, with their stored passwords, the users the
// panel made. It returns where the admin statements go, for the access.
func (s *Service) recreateUsers(ctx context.Context, d Database) (recreatedUsers, dbquery.Target, accountSQL, error) {
	out := recreatedUsers{ok: map[string]bool{}, errs: map[string]string{}}
	_, t, a, err := s.adminRead(ctx, d.ID)
	if err != nil {
		return out, t, nil, err
	}
	users, err := s.d.Store.ListUsers(ctx, d.ID)
	if err != nil {
		return out, t, a, err
	}
	for _, u := range users {
		if err := s.d.Admin.Exec(ctx, t, d.Engine, defaultDB(d.Engine), a.CreateUser(u.Name, u.Password)); err != nil {
			out.failed = append(out.failed, u.Name)
			out.errs[u.Name] = scrubErr(err, d.Password, u.Password).Error()
			continue
		}
		out.ok[u.Name] = true
	}
	return out, t, a, nil
}

// recreateDatabase restores one Backup on the new server, as it was backed
// up. A database the new server already has (postgres) is replaced: there
// is nothing in it to keep.
func (s *Service) recreateDatabase(ctx context.Context, d Database, dp Dumper, p recreatePick) (string, error) {
	target := p.backup.LogicalName
	st, log, err := s.captureState(ctx, d, dp, target)
	if err != nil {
		return log, err
	}
	note := ""
	switch {
	case st.Prev != "":
	case p.backup.State.Prev == "":
		note = statelessNote(d.Engine, target)
	default:
		st.From, note = s.createdAs(ctx, d, dp, target, p.backup.State)
	}
	log, err = s.restoreInto(ctx, d, dp, st, p.path)
	if note != "" {
		log = note + "\n" + log
	}
	return log, err
}
