package databases

import (
	"context"
	"errors"
	"strings"
	"time"

	"relist-panel/internal/dbquery"
)

// ErrPendingPassword is a reset while an earlier one is unsettled.
var ErrPendingPassword = errors.New("a previous superuser password change is not settled yet; it is retried when the Database starts")

const pendingWarning = "A superuser password change was interrupted and is not settled yet. It is retried when the panel or this Database starts."

// ResetSuperuserPassword gives the Database's superuser a new generated
// password and recreates the container with it (Redis included). confirm must
// equal the Database's name. It is a job: it answers at once and runs in the
// background, in an order that a crash never turns into a lock-out:
//
//  1. store the new password as pending beside the current one;
//  2. set it live, then prove it by logging in with it;
//  3. make it the current password and clear pending;
//  4. recreate the container with the new environment.
//
// At the next start resolvePending probes both passwords for a Database that
// still has a pending one.
func (s *Service) ResetSuperuserPassword(ctx context.Context, id int64, confirm string) error {
	d, err := s.begin(ctx, id, JobKindResetPassword, func(d Database) error {
		if confirm != d.Name {
			return ErrConfirm
		}
		if d.PendingPassword != "" {
			return ErrPendingPassword
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		s.runReset(context.WithoutCancel(ctx), d)
	}()
	return nil
}

func (s *Service) runReset(ctx context.Context, d Database) {
	fail := func(err error, passwords ...string) {
		_ = s.d.Store.SetLastError(ctx, d.ID, "the superuser password was not changed: "+scrubAll(err.Error(), passwords...))
	}
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		fail(err)
		return
	}
	pw, err := newPassword()
	if err != nil {
		fail(err)
		return
	}
	// 1. pending
	if err := s.d.Store.SetPendingPassword(ctx, d.ID, pw); err != nil {
		fail(err, d.Password, pw)
		return
	}
	oldT := dbquery.Target{Host: ContainerName(d.ID), Port: eng.Port(), User: eng.User(), Password: d.Password}
	newT := oldT
	newT.Password = pw
	// 2. live, proved by a login with the new password
	var liveErr error
	if d.Engine == "redis" {
		liveErr = s.d.Admin.SetRedisPassword(ctx, oldT, pw)
	} else {
		a, err := accountsFor(d.Engine)
		if err != nil {
			_ = s.d.Store.ClearPending(ctx, d.ID)
			fail(err)
			return
		}
		liveErr = s.d.Admin.Exec(ctx, oldT, d.Engine, defaultDB(d.Engine), a.SuperuserPassword(pw))
	}
	if pingErr := s.d.Admin.Ping(ctx, newT, d.Engine); pingErr != nil {
		_ = s.d.Store.ClearPending(ctx, d.ID)
		if liveErr == nil {
			liveErr = pingErr
		}
		fail(liveErr, d.Password, pw)
		return
	}
	// 3. current
	if err := s.d.Store.PromotePending(ctx, d.ID); err != nil {
		// The new password is live and still pending: resolvePending settles it.
		fail(err, d.Password, pw)
		return
	}
	// 4. recreate
	if err := s.applyPassword(ctx, d.ID); err != nil {
		_ = s.d.Store.SetLastError(ctx, d.ID, scrubAll(err.Error(), d.Password, pw))
	}
}

// applyPassword rewrites the superuser's entry in the applied spec's env with
// the current password and reconciles, which recreates the container because
// the spec hash changed. Only the Env is rewritten: reconcile never reads
// specFor, so nothing else of the applied settings moves.
func (s *Service) applyPassword(ctx context.Context, id int64) error {
	unlock := s.lock(id)
	defer unlock()
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.DeletedAt != nil {
		return ErrNotFound
	}
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		return err
	}
	spec, err := appliedSpec(d)
	if err != nil {
		s.record(ctx, d, err)
		return err
	}
	spec.Env = swapEnv(spec.Env, eng.Env(d.Password))
	if err := s.saveApplied(ctx, &d, spec); err != nil {
		s.record(ctx, d, err)
		return err
	}
	_, err = s.reconcile(ctx, d)
	return err
}

// swapEnv replaces each entry of env whose key (the text before =) is one of
// want's, and appends the want entries that env has no key for.
func swapEnv(env, want []string) []string {
	key := func(e string) string { k, _, _ := strings.Cut(e, "="); return k }
	out := append([]string(nil), env...)
	for _, w := range want {
		replaced := false
		for i, e := range out {
			if key(e) == key(w) {
				out[i], replaced = w, true
			}
		}
		if !replaced {
			out = append(out, w)
		}
	}
	return out
}

// ResolvePendingPasswords settles, in the background, every running Database
// that has a pending password. Call it after ReconcileAll: it must not hold
// up what follows, since a container that is still starting is waited for.
func (s *Service) ResolvePendingPasswords(ctx context.Context) error {
	if s.locked(ctx) {
		return nil
	}
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, d := range all {
		if d.DeletedAt == nil && d.PendingPassword != "" && d.Desired == DesiredRunning {
			s.goResolve(ctx, d.ID)
		}
	}
	return nil
}

// goResolve runs resolvePending on its own goroutine, never under the
// Database's lock: applyPassword takes it.
func (s *Service) goResolve(ctx context.Context, id int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.resolvePending(ctx, id)
	}()
}

// resolvePending finds which of a Database's two passwords works. It waits
// for the container, up to pendingTries times pendingRetry. The new one
// working makes it current and recreates the container; the old one working
// drops the pending one; neither keeps it pending, with a warning on the
// Database.
func (s *Service) resolvePending(ctx context.Context, id int64) {
	for i := 0; i < s.pendingTries; i++ {
		d, err := s.d.Store.Get(ctx, id)
		if err != nil || d.DeletedAt != nil || d.PendingPassword == "" || s.Jobs(id) == JobKindResetPassword {
			return
		}
		if st, err := s.d.Host.Inspect(ctx, ContainerName(id)); err == nil && st.Exists && st.Running {
			if eng, err := Lookup(d.Engine, d.Version); err == nil {
				t := dbquery.Target{Host: ContainerName(id), Port: eng.Port(), User: eng.User(), Password: d.PendingPassword}
				if s.d.Admin.Ping(ctx, t, d.Engine) == nil {
					if err := s.d.Store.PromotePending(ctx, id); err != nil {
						return
					}
					if err := s.applyPassword(ctx, id); err != nil {
						_ = s.d.Store.SetLastError(ctx, id, scrubAll(err.Error(), d.Password, d.PendingPassword))
					}
					return
				}
				t.Password = d.Password
				if s.d.Admin.Ping(ctx, t, d.Engine) == nil {
					_ = s.d.Store.ClearPending(ctx, id)
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.pendingRetry):
		}
	}
}
