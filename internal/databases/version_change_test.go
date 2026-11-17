package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pgChangeRig is a running postgres 16 Database with logical databases app
// and shop, scripted for a version change: dumps print "<db> data", the roles
// dump prints rolesDump, and every admin SQL and restore stdin is kept.
type pgChangeRig struct {
	*rig
	d        Database
	admin    *[]string
	restores *[]string
}

func newPGChangeRig(t *testing.T) *pgChangeRig {
	t.Helper()
	r := newRig(t)
	r.svc.healthPoll, r.svc.healthTimeout = time.Millisecond, 50*time.Millisecond
	d := r.create(t, "Shop", "postgres", "16")
	d, _ = r.store.Get(context.Background(), d.ID)
	r.host.script(pgList, func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, "app\nshop\n")
		return 0, err
	})
	r.host.script("pg_dumpall -U postgres --roles-only", func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, rolesDump)
		return 0, err
	})
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, pgDB(req)+" data;\n")
		return 0, err
	})
	pr := &pgChangeRig{rig: r, d: d}
	pr.admin = r.stateAdmin("app_owner\n", "")
	pr.restores = r.captureRestore(pgRestore, "")
	r.host.reset()
	return pr
}

func (r *pgChangeRig) change(t *testing.T, to string) VersionChange {
	t.Helper()
	c, err := r.svc.ChangeVersion(context.Background(), r.d.ID, to, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != JobRunning || c.FromVersion != r.d.Version || c.ToVersion != to || c.Method != VersionMethodDumpRestore {
		t.Fatalf("returned = %+v", c)
	}
	r.svc.Wait()
	got, err := r.store.GetVersionChange(context.Background(), r.d.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// index is the position of the first call starting with prefix, or -1.
func index(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func lastIndex(calls []string, prefix string) int {
	at := -1
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			at = i
		}
	}
	return at
}

func TestChangeVersionPostgresSucceeds(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	c := r.change(t, "18")
	if c.Status != JobSucceeded || c.FromVolume != "relist-db-1" || c.Error != "" {
		t.Fatalf("row = %+v", c)
	}
	to := targetVolume(r.d.ID, c.ID)
	calls := r.host.all()
	order := []int{
		index(calls, "pull postgres:18"),
		index(calls, "disconnect relist-db-1"),
		index(calls, "exec relist-db-1 pg_dump -U postgres"),
		index(calls, "exec relist-db-1 pg_dumpall"),
		index(calls, "stop relist-db-1"),
		index(calls, "create relist-db-1"),
		index(calls, "exec relist-db-1 "+pgRestore),
	}
	for i := 1; i < len(order); i++ {
		if order[i-1] < 0 || order[i] <= order[i-1] {
			t.Fatalf("call order %v in %v", order, calls)
		}
	}
	got, _ := r.store.Get(ctx, r.d.ID)
	spec, err := appliedSpec(got)
	if err != nil || got.Version != "18" || got.Volume != to || spec.Image != "postgres:18" || spec.DataPath != "/var/lib/postgresql" || spec.Volume != to {
		t.Fatalf("database = %+v, spec %+v, %v", got, spec, err)
	}
	if r.host.specs["relist-db-1"].Volume != to {
		t.Fatalf("container volume = %q", r.host.specs["relist-db-1"].Volume)
	}
	if len(*r.restores) != 2 || (*r.restores)[0] != "app data;\n" || (*r.restores)[1] != "shop data;\n" {
		t.Fatalf("restores = %q", *r.restores)
	}
	var roles string
	for _, s := range *r.admin {
		if strings.Contains(s, "CREATE ROLE full_u;") {
			roles = s
		}
	}
	if roles == "" || strings.Contains(roles, "CREATE ROLE postgres;") {
		t.Fatalf("roles restore = %q", roles)
	}
	info, err := os.Stat(filepath.Join(r.dataDir, ".databases", fmt.Sprint(r.d.ID), "backups", fmt.Sprintf("version-change-%d-roles.sql", c.ID)))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("roles file: %v %v", info, err)
	}
	for _, want := range []string{"→ Pull postgres:18", "→ Back up app", "→ Back up the roles", "→ Restore shop", "→ Switch to postgres:18"} {
		if !strings.Contains(c.Log, want) {
			t.Fatalf("log lacks %q:\n%s", want, c.Log)
		}
	}
	if r.svc.Jobs(r.d.ID) != "" {
		t.Fatal("slot not released")
	}
}

func TestChangeVersionRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		setup func(r *pgChangeRig)
		to    string
		want  error
	}{
		{"same version", nil, "16", ErrNotHigher},
		{"unknown tag", nil, "19", ErrUnknownVersion},
		{"pending password", func(r *pgChangeRig) { _ = r.store.SetPendingPassword(ctx, r.d.ID, "new-pw") }, "18", ErrPendingPassword},
		{"stopped", func(r *pgChangeRig) { _ = r.svc.Stop(ctx, r.d.ID) }, "18", ErrNotRunning},
		{"busy", func(r *pgChangeRig) { _ = r.svc.beginJob(r.d.ID, JobKindBackup) }, "18", ErrBusy},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newPGChangeRig(t)
			if c.setup != nil {
				c.setup(r)
			}
			r.host.reset()
			if _, err := r.svc.ChangeVersion(ctx, r.d.ID, c.to, false); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if list, _ := r.store.ListVersionChanges(ctx, r.d.ID); len(list) != 0 {
				t.Fatalf("rows = %+v", list)
			}
			if m := r.host.mutating(); len(m) != 0 {
				t.Fatalf("host calls = %v", m)
			}
		})
	}
}

func TestChangeVersionPullFailureNoDowntime(t *testing.T) {
	r := newPGChangeRig(t)
	r.host.failOn["pull postgres:18"] = errors.New("registry unreachable")
	c := r.change(t, "18")
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Pull postgres:18: ") {
		t.Fatalf("row = %+v", c)
	}
	for _, p := range []string{"disconnect ", "stop ", "create ", "remove "} {
		if i := index(r.host.all(), p); i >= 0 {
			t.Fatalf("%s happened: %v", p, r.host.all())
		}
	}
	got, _ := r.store.Get(context.Background(), r.d.ID)
	if got.Version != "16" || got.Volume != "relist-db-1" {
		t.Fatalf("database = %+v", got)
	}
}

// assertRolledBack checks the Database is back on postgres 16 on its volume.
func (r *pgChangeRig) assertRolledBack(t *testing.T, c VersionChange) {
	t.Helper()
	calls := r.host.all()
	to := targetVolume(r.d.ID, c.ID)
	if index(calls, "remove-volume "+to) < 0 {
		t.Fatalf("target volume not removed: %v", calls)
	}
	if lastIndex(calls, "create relist-db-1") < index(calls, "remove-volume "+to) {
		t.Fatalf("the old container was not created last: %v", calls)
	}
	if spec := r.host.specs["relist-db-1"]; spec.Image != "postgres:16" || spec.Volume != "relist-db-1" {
		t.Fatalf("last container = %+v", spec)
	}
	got, _ := r.store.Get(context.Background(), r.d.ID)
	if got.Version != "16" || got.Volume != "relist-db-1" {
		t.Fatalf("database = %+v", got)
	}
	if !strings.Contains(c.Log, "Rolled back to postgres:16 on relist-db-1") {
		t.Fatalf("log:\n%s", c.Log)
	}
}

func TestChangeVersionRestoreFailureRollsBack(t *testing.T) {
	r := newPGChangeRig(t)
	r.host.override(pgRestore, func(req ExecRequest) (int, error) {
		_, _ = io.ReadAll(req.Stdin)
		if pgDB(req) == "shop" {
			_, _ = io.WriteString(req.Stderr, "ERROR: boom\nLINE 1: ALTER ROLE x PASSWORD 'SCRAM-SHA-256$x'\n")
			return 1, nil
		}
		return 0, nil
	})
	c := r.change(t, "18")
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Restore shop: ") {
		t.Fatalf("row = %+v", c)
	}
	if strings.Contains(c.Error+c.Log, "SCRAM") {
		t.Fatalf("a password hash leaked: %q / %q", c.Error, c.Log)
	}
	if !strings.Contains(c.Log, "ERROR: boom") {
		t.Fatalf("the restore's stderr is not in the log:\n%s", c.Log)
	}
	r.assertRolledBack(t, c)
}

func TestChangeVersionUnhealthyRollsBack(t *testing.T) {
	r := newPGChangeRig(t)
	r.host.health["postgres:18"] = "unhealthy"
	c := r.change(t, "18")
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Start postgres:18 on ") {
		t.Fatalf("row = %+v", c)
	}
	r.assertRolledBack(t, c)
}

// A failure while the dumps run never stopped the old container: putting it
// back on the network is the whole rollback.
func TestChangeVersionBackupFailureReconnects(t *testing.T) {
	r := newPGChangeRig(t)
	r.host.failOn["exec relist-db-1 pg_dumpall"] = errors.New("exec failed")
	c := r.change(t, "18")
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Back up the roles: ") {
		t.Fatalf("row = %+v", c)
	}
	calls := r.host.all()
	if index(calls, "stop ") >= 0 || index(calls, "realias relist-db-1") < index(calls, "disconnect relist-db-1") {
		t.Fatalf("calls = %v", calls)
	}
}

// Review Focus 4: nothing else waits behind a long change; it is refused.
func TestChangeVersionBusyDoesNotBlock(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	release := make(chan struct{})
	r.host.override("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		<-release
		_, err := io.WriteString(req.Stdout, "x;\n")
		return 0, err
	})
	if _, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false); err != nil {
		t.Fatal(err)
	}
	defer func() { close(release); r.svc.Wait() }()
	for name, fn := range map[string]func() error{
		"stop":         func() error { return r.svc.Stop(ctx, r.d.ID) },
		"start":        func() error { return r.svc.Start(ctx, r.d.ID) },
		"restart":      func() error { return r.svc.Restart(ctx, r.d.ID) },
		"update image": func() error { return r.svc.UpdateImage(ctx, r.d.ID, false) },
		"delete":       func() error { return r.svc.Delete(ctx, r.d.ID, "Shop", true) },
		"change again": func() error { _, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false); return err },
	} {
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrBusy) {
				t.Errorf("%s: err = %v, want ErrBusy", name, err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("%s blocked behind the change", name)
		}
	}
}

// Review Focus 5: the rollback fails too.
func TestChangeVersionRollbackFailure(t *testing.T) {
	r := newPGChangeRig(t)
	r.host.override(pgRestore, func(req ExecRequest) (int, error) {
		_, _ = io.ReadAll(req.Stdin)
		r.host.mu.Lock()
		r.host.failOn["create relist-db-1"] = errors.New("disk full")
		r.host.mu.Unlock()
		return 1, nil
	})
	c := r.change(t, "18")
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Restore app: ") || !strings.Contains(c.Error, "disk full") {
		t.Fatalf("row = %+v", c)
	}
	if index(r.host.all(), "remove-volume "+targetVolume(r.d.ID, c.ID)) < 0 {
		t.Fatalf("target volume not removed: %v", r.host.all())
	}
	got, _ := r.store.Get(context.Background(), r.d.ID)
	if !strings.Contains(got.LastError, "Restore app") || !strings.Contains(got.LastError, "disk full") {
		t.Fatalf("LastError = %q", got.LastError)
	}
}

const mariaList = "mariadb -uroot -N -e SHOW DATABASES"

// inPlaceRig is a running Database of engine at version, scripted to list
// one logical database "app" and dump it.
func inPlaceRig(t *testing.T, engine, version string) (*rig, Database) {
	t.Helper()
	r := newRig(t)
	r.svc.healthPoll, r.svc.healthTimeout = time.Millisecond, 50*time.Millisecond
	d := r.create(t, "Shop", engine, version)
	d, _ = r.store.Get(context.Background(), d.ID)
	r.host.script(mariaList, func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, "app\n")
		return 0, err
	})
	r.dumps("mariadb-dump", "x;\n")
	r.host.reset()
	return r, d
}

func (r *rig) changeTo(t *testing.T, d Database, to string, backupFirst bool) VersionChange {
	t.Helper()
	c, err := r.svc.ChangeVersion(context.Background(), d.ID, to, backupFirst)
	if err != nil {
		t.Fatal(err)
	}
	if c.Method != VersionMethodInPlace {
		t.Fatalf("method = %q", c.Method)
	}
	r.svc.Wait()
	got, err := r.store.GetVersionChange(context.Background(), d.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func hasEnv(spec ContainerSpec, kv string) bool {
	for _, e := range spec.Env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestChangeVersionMariaDBInPlace(t *testing.T) {
	r, d := inPlaceRig(t, "mariadb", "11.4")
	ctx := context.Background()
	before := len(r.host.created)
	c := r.changeTo(t, d, "11.8", true)
	if c.Status != JobSucceeded || c.FromVolume != "" {
		t.Fatalf("row = %+v", c)
	}
	bl, _ := r.store.ListBackups(ctx, d.ID)
	if len(bl) != 1 || bl[0].Trigger != TriggerPreUpdate || bl[0].Status != JobSucceeded {
		t.Fatalf("backups = %+v", bl)
	}
	creates := r.host.created[before:]
	if len(creates) != 2 || !hasEnv(creates[0], "MARIADB_AUTO_UPGRADE=1") || hasEnv(creates[1], "MARIADB_AUTO_UPGRADE=1") ||
		creates[0].Image != "mariadb:11.8" || creates[1].Image != "mariadb:11.8" || creates[1].Volume != d.Volume {
		t.Fatalf("creates = %+v", creates)
	}
	if creates[0].Labels[HashLabel] == creates[1].Labels[HashLabel] {
		t.Fatal("the upgrade container carries the plain container's hash")
	}
	got, _ := r.store.Get(ctx, d.ID)
	spec, _ := appliedSpec(got)
	if got.Version != "11.8" || got.Volume != d.Volume || hasEnv(spec, "MARIADB_AUTO_UPGRADE=1") || spec.Image != "mariadb:11.8" {
		t.Fatalf("database = %+v, spec %+v", got, spec)
	}
	if v := r.view(t, d.ID); v.SettingsOutdated {
		t.Fatal("settings outdated after the change")
	}
}

func TestChangeVersionRedisInPlace(t *testing.T) {
	r, d := inPlaceRig(t, "redis", "7.4")
	c := r.changeTo(t, d, "8", true)
	if c.Status != JobSucceeded {
		t.Fatalf("row = %+v", c)
	}
	if bl, _ := r.store.ListBackups(context.Background(), d.ID); len(bl) != 0 {
		t.Fatalf("backups = %+v", bl)
	}
	got, _ := r.store.Get(context.Background(), d.ID)
	if got.Version != "8" || r.host.specs[ContainerName(d.ID)].Image != "redis:8" {
		t.Fatalf("database = %+v", got)
	}
}

// The new version may have rewritten the data already, which the old one
// cannot read (redis 7.4 refuses an RDB redis 8 saved): no rollback.
func TestChangeVersionInPlaceNoRollback(t *testing.T) {
	r, d := inPlaceRig(t, "mariadb", "11.4")
	r.host.health["mariadb:11.8"] = "unhealthy"
	before := len(r.host.created)
	c := r.changeTo(t, d, "11.8", true)
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Start mariadb:11.8: ") {
		t.Fatalf("row = %+v", c)
	}
	for _, spec := range r.host.created[before:] {
		if spec.Image != "mariadb:11.8" {
			t.Fatalf("recreated %s after the switch", spec.Image)
		}
	}
	got, _ := r.store.Get(context.Background(), d.ID)
	bl, _ := r.store.ListBackups(context.Background(), d.ID)
	if got.Version != "11.8" || !strings.Contains(got.LastError, "It is not rolled back") ||
		len(bl) != 1 || !strings.Contains(got.LastError, fmt.Sprintf("#%d", bl[0].ID)) {
		t.Fatalf("database = %+v", got)
	}
}

func TestChangeVersionInPlaceBackupFailureChangesNothing(t *testing.T) {
	r, d := inPlaceRig(t, "mariadb", "11.4")
	r.host.failOn["exec "+ContainerName(d.ID)+" mariadb-dump"] = errors.New("dump failed")
	c := r.changeTo(t, d, "11.8", true)
	if c.Status != JobFailed || !strings.HasPrefix(c.Error, "Back up app: ") {
		t.Fatalf("row = %+v", c)
	}
	if index(r.host.all(), "pull ") >= 0 || index(r.host.all(), "create ") >= 0 {
		t.Fatalf("calls = %v", r.host.all())
	}
	got, _ := r.store.Get(context.Background(), d.ID)
	if got.Version != "11.4" {
		t.Fatalf("database = %+v", got)
	}
}

// Review Focus 3: the panel stopped half-way through a change, after the new
// container was created but before the switch.
func TestInterruptedChangeRecovers(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "16")
	d, _ = r.store.Get(ctx, d.ID)
	c, err := r.store.InsertVersionChange(ctx, VersionChange{DatabaseID: d.ID, FromVersion: "16", ToVersion: "18", Method: VersionMethodDumpRestore})
	if err != nil {
		t.Fatal(err)
	}
	to := targetVolume(d.ID, c.ID)
	if err := r.store.SetVersionChangeTarget(ctx, c.ID, to); err != nil {
		t.Fatal(err)
	}
	next := d
	next.Version, next.Volume = "18", to
	spec, _, _ := specFor(next)
	if err := r.host.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SweepFailedVersionVolumes(ctx); err != nil {
		t.Fatal(err)
	}
	row, _ := r.store.GetVersionChange(ctx, d.ID, c.ID)
	if row.Status != JobFailed || row.Error != interrupted {
		t.Fatalf("row = %+v", row)
	}
	if got := r.host.specs[ContainerName(d.ID)]; got.Image != "postgres:16" || got.Volume != "relist-db-1" {
		t.Fatalf("container = %+v", got)
	}
	if !r.host.saw("remove-volume "+to) || r.host.volumes[to] {
		t.Fatalf("target volume kept: %v", r.host.all())
	}
}

// Review: the new server joins relist-network only after the switch, or apps
// would write into it while the restore still runs, and those writes would
// be lost on a rollback or collide with the restored rows.
func TestChangeVersionNewServerOffNetworkUntilSwitch(t *testing.T) {
	r := newPGChangeRig(t)
	name := ContainerName(r.d.ID)
	var onNetwork []bool
	r.host.override(pgRestore, func(req ExecRequest) (int, error) {
		_, _ = io.ReadAll(req.Stdin)
		r.host.mu.Lock()
		onNetwork = append(onNetwork, r.host.containers[name].OnNetwork)
		r.host.mu.Unlock()
		return 0, nil
	})
	c := r.change(t, "18")
	if c.Status != JobSucceeded {
		t.Fatalf("row = %+v", c)
	}
	if len(onNetwork) != 2 || onNetwork[0] || onNetwork[1] {
		t.Fatalf("on the network during the restores: %v", onNetwork)
	}
	calls := r.host.all()
	if lastIndex(calls, "realias "+name) < lastIndex(calls, "exec "+name+" "+pgRestore) || !r.host.containers[name].OnNetwork {
		t.Fatalf("not reconnected after the switch: %v", calls)
	}
}

// Review: a Rename re-aliases the container, which would put the old server
// back on the network in the middle of the dumps.
func TestRenameRefusedDuringChange(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	release := make(chan struct{})
	r.host.override("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		<-release
		_, err := io.WriteString(req.Stdout, "x;\n")
		return 0, err
	})
	if _, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false); err != nil {
		t.Fatal(err)
	}
	err := r.svc.Rename(ctx, r.d.ID, "Blog")
	close(release)
	r.svc.Wait()
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Rename = %v, want ErrBusy", err)
	}
	if got, _ := r.store.Get(ctx, r.d.ID); got.Name != "Shop" {
		t.Fatalf("renamed to %q", got.Name)
	}
}
