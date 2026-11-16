package databases

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func (r *adminRig) fast() {
	r.svc.pendingRetry = time.Millisecond
	r.svc.pendingTries = 3
}

// appliedEnv is the env of the applied spec stored for Database id.
func (r *adminRig) appliedEnv(t *testing.T, id int64) []string {
	t.Helper()
	d, err := r.store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var spec ContainerSpec
	if err := json.Unmarshal([]byte(d.AppliedSpec), &spec); err != nil {
		t.Fatal(err)
	}
	return spec.Env
}

func TestResetSuperuserPostgres(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.pingDefault = true
	r.host.reset()
	if err := r.svc.ResetSuperuserPassword(ctx, d.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	if j := r.svc.Jobs(d.ID); j != "reset_password" && j != "" {
		t.Fatalf("Jobs = %q", j)
	}
	r.svc.Wait()
	got, _ := r.store.Get(ctx, d.ID)
	if got.Password == d.Password || len(got.Password) != 43 || got.PendingPassword != "" || got.LastError != "" {
		t.Fatalf("database = %+v", got)
	}
	stmts := r.admin.stmts()
	if len(stmts) != 1 || stmts[0] != "ALTER ROLE postgres WITH PASSWORD '"+got.Password+"'" {
		t.Fatalf("statements = %q", stmts)
	}
	if tg := r.admin.targets[0]; tg.Password != d.Password {
		t.Fatalf("the ALTER must log in with the old password: %+v", tg)
	}
	if env := r.appliedEnv(t, d.ID); !slices.Equal(env, []string{"POSTGRES_PASSWORD=" + got.Password}) {
		t.Fatalf("applied env = %q", env)
	}
	if n := r.host.count("create relist-db-1"); n != 1 {
		t.Fatalf("container created %d times, want 1: %v", n, r.host.all())
	}
	if v := r.view(t, d.ID); v.SettingsOutdated || v.Status != StatusRunning || len(v.Warnings) != 0 {
		t.Fatalf("view = %+v", v)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

func TestResetSuperuserMySQL(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Blog", "mariadb", "11.8")
	r.admin.pingDefault = true
	if err := r.svc.ResetSuperuserPassword(context.Background(), d.ID, "Blog"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got, _ := r.store.Get(context.Background(), d.ID)
	if st := r.admin.stmts(); len(st) != 1 || !strings.Contains(st[0], "'root'@'localhost'") {
		t.Fatalf("statements = %q", st)
	}
	if env := r.appliedEnv(t, d.ID); !slices.Equal(env, []string{"MARIADB_ROOT_PASSWORD=" + got.Password}) {
		t.Fatalf("env = %q", env)
	}
}

func TestResetSuperuserRedis(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Cache", "redis", "8")
	r.admin.pingDefault = true
	if err := r.svc.ResetSuperuserPassword(ctx, d.ID, "Cache"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got, _ := r.store.Get(ctx, d.ID)
	if len(r.admin.redisSet) != 1 || r.admin.redisSet[0] != got.Password || got.Password == d.Password || got.PendingPassword != "" {
		t.Fatalf("redisSet = %q, database = %+v", r.admin.redisSet, got)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("redis sent SQL: %q", r.admin.stmts())
	}
	if env := r.appliedEnv(t, d.ID); !slices.Equal(env, []string{"REDIS_PASSWORD=" + got.Password}) {
		t.Fatalf("env = %q", env)
	}
}

func TestResetSuperuserLiveFails(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	var sent string
	r.admin.execErr = func(_ string, stmts []string) error {
		sent = stmts[0]
		return errors.New("failed: " + stmts[0] + " as " + d.Password)
	}
	r.host.reset()
	if err := r.svc.ResetSuperuserPassword(ctx, d.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got, _ := r.store.Get(ctx, d.ID)
	nw := sent[strings.Index(sent, "'")+1 : len(sent)-1]
	if got.Password != d.Password || got.PendingPassword != "" {
		t.Fatalf("password changed or pending left: %+v", got)
	}
	if !strings.HasPrefix(got.LastError, "the superuser password was not changed: ") ||
		strings.Contains(got.LastError, nw) || strings.Contains(got.LastError, d.Password) {
		t.Fatalf("LastError = %q", got.LastError)
	}
	if r.host.count("create ") != 0 {
		t.Fatal("the container was recreated for a password that never took")
	}
}

// The server took the new password although the statement reported an error
// (the answer was lost): carry on.
func TestResetSuperuserCarriesOnWhenNewWorks(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.pingDefault = true
	r.admin.execErr = func(string, []string) error { return errors.New("connection reset") }
	_ = r.svc.ResetSuperuserPassword(ctx, d.ID, "Shop")
	r.svc.Wait()
	got, _ := r.store.Get(ctx, d.ID)
	if got.Password == d.Password || got.PendingPassword != "" {
		t.Fatalf("database = %+v", got)
	}
}

func TestResetSuperuserConfirm(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.ResetSuperuserPassword(ctx, d.ID, "shop"); !errors.Is(err, ErrConfirm) {
		t.Fatalf("err = %v", err)
	}
	if r.svc.Jobs(d.ID) != "" || len(r.admin.calls()) != 0 {
		t.Fatal("a refused reset started")
	}
	if got, _ := r.store.Get(ctx, d.ID); got.PendingPassword != "" {
		t.Fatal("pending set")
	}
}

func TestResetSuperuserBusyAndPending(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	_ = r.svc.beginJob(d.ID, JobKindBackup)
	if err := r.svc.ResetSuperuserPassword(ctx, d.ID, "Shop"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy = %v", err)
	}
	r.svc.endJob(d.ID)
	_ = r.store.SetPendingPassword(ctx, d.ID, "x")
	err := r.svc.ResetSuperuserPassword(ctx, d.ID, "Shop")
	if !errors.Is(err, ErrPendingPassword) || err.Error() != "a previous superuser password change is not settled yet; it is retried when the Database starts" {
		t.Fatalf("pending = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatal("statements sent")
	}
}

func TestResetSuperuserNeedsRunning(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.containers["relist-db-1"].Running = false
	if err := r.svc.ResetSuperuserPassword(context.Background(), d.ID, "Shop"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestSwapEnv(t *testing.T) {
	got := swapEnv([]string{"A=1", "POSTGRES_PASSWORD=old", "B=2"}, []string{"POSTGRES_PASSWORD=new"})
	if !slices.Equal(got, []string{"A=1", "POSTGRES_PASSWORD=new", "B=2"}) {
		t.Fatalf("replace = %q", got)
	}
	got = swapEnv([]string{"A=1"}, []string{"POSTGRES_PASSWORD=new", "C=3"})
	if !slices.Equal(got, []string{"A=1", "POSTGRES_PASSWORD=new", "C=3"}) {
		t.Fatalf("append = %q", got)
	}
	// A value that holds an = is not split wrongly.
	got = swapEnv([]string{"K=a=b"}, []string{"K=c=d"})
	if !slices.Equal(got, []string{"K=c=d"}) {
		t.Fatalf("= in a value = %q", got)
	}
}

// Review Focus 4: a crash between "set live" and "make current" leaves the
// new password pending; the next start must find which one works.
func TestResolvePendingOutcomes(t *testing.T) {
	const pendingWarning = "A superuser password change was interrupted and is not settled yet. It is retried when the panel or this Database starts."
	for _, tc := range []struct {
		name        string
		pingOK      map[string]bool
		wantPass    string
		wantPending string
		wantCreates int
		wantWarning bool
	}{
		{"new works", map[string]bool{"new": true}, "new", "", 1, false},
		{"old works", map[string]bool{"old": true}, "old", "", 0, false},
		{"neither works", map[string]bool{}, "old", "new", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAdminRig(t)
			r.fast()
			ctx := context.Background()
			d := r.create(t, "Shop", "postgres", "18")
			// Make the stored password "old" without a recreate.
			if _, err := r.store.db.Exec(`UPDATE databases SET password = 'old' WHERE id = ?`, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := r.store.SetPendingPassword(ctx, d.ID, "new"); err != nil {
				t.Fatal(err)
			}
			r.admin.pingOK = tc.pingOK
			r.host.reset()
			r.svc.resolvePending(ctx, d.ID)
			got, _ := r.store.Get(ctx, d.ID)
			if got.Password != tc.wantPass || got.PendingPassword != tc.wantPending {
				t.Fatalf("database = %+v", got)
			}
			if n := r.host.count("create relist-db-1"); n != tc.wantCreates {
				t.Fatalf("creates = %d, want %d", n, tc.wantCreates)
			}
			if tc.wantCreates == 1 {
				if env := r.appliedEnv(t, d.ID); !slices.Equal(env, []string{"POSTGRES_PASSWORD=new"}) {
					t.Fatalf("applied env = %q", env)
				}
			}
			v := r.view(t, d.ID)
			has := slices.Contains(v.Warnings, pendingWarning)
			if has != tc.wantWarning {
				t.Fatalf("warnings = %q, want warning %v", v.Warnings, tc.wantWarning)
			}
		})
	}
}

func TestResolvePendingWaitsForTheContainer(t *testing.T) {
	r := newAdminRig(t)
	r.fast()
	r.svc.pendingTries = 50
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	_ = r.store.SetPendingPassword(ctx, d.ID, "new")
	r.admin.pingOK = map[string]bool{"new": true}
	r.host.mu.Lock()
	r.host.containers["relist-db-1"].Running = false
	r.host.mu.Unlock()
	go func() {
		time.Sleep(20 * time.Millisecond)
		r.host.mu.Lock()
		r.host.containers["relist-db-1"].Running = true
		r.host.mu.Unlock()
	}()
	r.svc.resolvePending(ctx, d.ID)
	if got, _ := r.store.Get(ctx, d.ID); got.Password != "new" || got.PendingPassword != "" {
		t.Fatalf("database = %+v", got)
	}
}

func TestResolvePendingPasswordsAtStart(t *testing.T) {
	r := newAdminRig(t)
	r.fast()
	ctx := context.Background()
	a := r.create(t, "A", "postgres", "18")
	b := r.create(t, "B", "postgres", "18")
	_ = r.store.SetPendingPassword(ctx, a.ID, "new")
	r.admin.pingOK = map[string]bool{"new": true}
	if err := r.svc.ResolvePendingPasswords(ctx); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	ga, _ := r.store.Get(ctx, a.ID)
	gb, _ := r.store.Get(ctx, b.ID)
	if ga.Password != "new" || ga.PendingPassword != "" || gb.Password != b.Password {
		t.Fatalf("a = %+v, b = %+v", ga, gb)
	}
}

func TestStartResolvesPending(t *testing.T) {
	r := newAdminRig(t)
	r.fast()
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	_ = r.store.SetPendingPassword(ctx, d.ID, "new")
	r.admin.pingOK = map[string]bool{"new": true}
	if err := r.svc.Start(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if got, _ := r.store.Get(ctx, d.ID); got.Password != "new" || got.PendingPassword != "" {
		t.Fatalf("database = %+v", got)
	}
}
