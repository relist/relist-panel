package databases

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// loseVolume removes d's container and volume behind the panel's back, the
// way a `docker volume prune` or a lost disk would.
func (r *rig) loseVolume(t *testing.T, id int64) {
	t.Helper()
	d, err := r.store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	r.host.mu.Lock()
	defer r.host.mu.Unlock()
	delete(r.host.containers, ContainerName(id))
	delete(r.host.volumes, volumeOf(d))
}

// recreateRig is a Postgres Database "Shop" with two managed users and one
// Backup of "app", whose state names app_owner the owner and reader a
// Read-only user, and whose volume is then lost. fresh flips the admin
// script to a new, empty server.
type recreateRig struct {
	*adminRig
	d        Database
	b        Backup
	sql      *[]string // the docker exec admin SQL, in order
	restored *[]string
	fresh    *bool
}

func newRecreateRig(t *testing.T) *recreateRig {
	t.Helper()
	r := newAdminRig(t)
	r.svc.healthPoll, r.svc.healthTimeout = time.Millisecond, 50*time.Millisecond
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "CREATE TABLE t ();\n")
	var sql []string
	fresh := false
	r.host.script(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		s := string(b)
		sql = append(sql, s)
		switch {
		case strings.Contains(s, "rolsuper"):
			_, _ = io.WriteString(req.Stdout, "f\n")
		case strings.Contains(s, "datname = 'app';") && !fresh:
			_, _ = io.WriteString(req.Stdout, "app_owner\t{app_owner=CTc/app_owner,reader=c/app_owner}\nreader\tread\napp_owner\tfull\n")
		}
		return 0, nil
	})
	for _, u := range []string{"app_owner", "reader"} {
		if err := r.store.UpsertUser(ctx, d.ID, u, "pw-"+u); err != nil {
			t.Fatal(err)
		}
	}
	b := r.manualBackup(t, d.ID)
	if b.State.Access["reader"] != AccessRead {
		t.Fatalf("state = %+v", b.State)
	}
	restored := r.captureRestore(pgRestore, "")
	r.loseVolume(t, d.ID)
	if v := r.view(t, d.ID); v.Status != StatusVolumeMissing {
		t.Fatalf("status = %s", v.Status)
	}
	fresh = true
	// The new server as the Users tab reads it.
	pg := mustAccounts(t, "postgres")
	r.admin.answer(pg.UsersQuery(), [][]string{{"app_owner"}, {"reader"}})
	r.admin.answer(pg.DatabasesQuery(), [][]string{{"app", "1", "app_owner", "f"}})
	r.admin.answer(pg.SuperuserRolesQuery(), [][]string{{"postgres"}})
	r.admin.answerOn("app", pg.SchemasQuery(), [][]string{{"public"}})
	r.host.reset()
	return &recreateRig{adminRig: r, d: d, b: b, sql: &sql, restored: restored, fresh: &fresh}
}

// Recreate makes a new volume, starts the server with the Database's
// password, gives the users the panel made their passwords back, restores
// each picked Backup as it was backed up, then gives each user its access.
func TestRecreateRestoresUsersDatabasesAndAccess(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	*r.sql = nil
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()

	if r.host.count("create relist-db-1") != 1 {
		t.Fatalf("host calls = %v", r.host.all())
	}
	got, _ := r.store.Get(ctx, r.d.ID)
	if got.Password != r.d.Password || got.LastError != "" {
		t.Fatalf("record = %+v", got)
	}
	stmts := strings.Join(r.admin.stmts(), "\n")
	for _, want := range []string{
		`CREATE ROLE "app_owner" LOGIN PASSWORD 'pw-app_owner'`,
		`CREATE ROLE "reader" LOGIN PASSWORD 'pw-reader'`,
		`GRANT CONNECT ON DATABASE "app" TO "reader"`,
	} {
		if !strings.Contains(stmts, want) {
			t.Fatalf("admin statements lack %q:\n%s", want, stmts)
		}
	}
	if i, j := strings.Index(stmts, `CREATE ROLE "app_owner"`), strings.Index(stmts, `GRANT CONNECT`); i > j {
		t.Fatalf("access was given before the users existed:\n%s", stmts)
	}
	all := strings.Join(*r.sql, "\n")
	if !strings.Contains(all, `CREATE DATABASE "app" TEMPLATE template0 OWNER "app_owner";`) {
		t.Fatalf("the database was not created as it was backed up:\n%s", all)
	}
	if len(*r.restored) != 1 || (*r.restored)[0] != "CREATE TABLE t ();\n" {
		t.Fatalf("restored = %q", *r.restored)
	}
	rs := r.lastRestore(t, r.d.ID)
	if rs.Status != JobSucceeded || rs.Target != "app" || rs.BackupID == nil || *rs.BackupID != r.b.ID {
		t.Fatalf("restore = %+v", rs)
	}
	if r.svc.Jobs(r.d.ID) != "" {
		t.Fatal("the job slot is still held")
	}
}

// Every check runs before anything is created: a refused Recreate leaves
// the host alone.
func TestRecreateRefusesBeforeTouchingTheHost(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	other := r.manualBackupOf(t, r.b)

	if err := r.svc.Recreate(ctx, r.d.ID, "shop", []int64{r.b.ID}); !errors.Is(err, ErrConfirm) {
		t.Errorf("wrong name: err = %v", err)
	}
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{999}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown Backup: err = %v", err)
	}
	var twice ErrSameDatabaseTwice
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID, other}); !errors.As(err, &twice) || twice.Name != "app" {
		t.Errorf("same database twice: err = %v", err)
	}

	// A file that changed on disk.
	path := filepath.Join(r.backupsDir(r.d.ID), r.b.FileName)
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); !errors.Is(err, ErrChangedFile) {
		t.Errorf("changed file: err = %v", err)
	}
	if r.host.count("create") != 0 || r.svc.Jobs(r.d.ID) != "" {
		t.Fatalf("a refused Recreate touched the host or kept the slot: %v", r.host.all())
	}

	// A Database whose volume is there is not recreated.
	r2 := newAdminRig(t)
	d2 := r2.create(t, "Live", "postgres", "18")
	r2.host.reset()
	if err := r2.svc.Recreate(ctx, d2.ID, "Live", nil); !errors.Is(err, ErrVolumeNotMissing) {
		t.Fatalf("live Database: err = %v", err)
	}
	if r2.host.count("create") != 0 {
		t.Fatalf("host calls = %v", r2.host.all())
	}

	// Redis keeps no Backups.
	d3 := r2.create(t, "Cache", "redis", "8")
	if err := r2.svc.Recreate(ctx, d3.ID, "Cache", nil); !errors.Is(err, ErrNoBackups) {
		t.Fatalf("redis: err = %v", err)
	}
}

// manualBackupOf takes another Backup of the same logical database as b.
func (r *recreateRig) manualBackupOf(t *testing.T, b Backup) int64 {
	t.Helper()
	ctx := context.Background()
	nb, err := r.store.InsertBackup(ctx, Backup{DatabaseID: b.DatabaseID, Trigger: TriggerManual, LogicalName: b.LogicalName})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(r.backupsDir(b.DatabaseID), b.FileName)
	name := "other-" + b.FileName
	raw, _ := os.ReadFile(src)
	_ = os.WriteFile(filepath.Join(r.backupsDir(b.DatabaseID), name), raw, 0o600)
	if err := r.store.FinishBackup(ctx, nb.ID, Backup{Status: JobSucceeded, FileName: name, Size: b.Size, SHA256: b.SHA256}); err != nil {
		t.Fatal(err)
	}
	return nb.ID
}

// A restore that fails leaves the Database running with the failed Restore
// and a last error naming it; nothing is rolled back.
func TestRecreateKeepsGoingAfterAFailedRestore(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	r.host.override(pgRestore, func(req ExecRequest) (int, error) {
		_, _ = io.Copy(io.Discard, req.Stdin)
		_, _ = io.WriteString(req.Stderr, `ERROR: role "ghost" does not exist`)
		return 3, nil
	})
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	rs := r.lastRestore(t, r.d.ID)
	if rs.Status != JobFailed || !strings.Contains(rs.Log, "ghost") {
		t.Fatalf("restore = %+v", rs)
	}
	got, _ := r.store.Get(ctx, r.d.ID)
	if !strings.Contains(got.LastError, "app") || strings.Contains(got.LastError, r.d.Password) {
		t.Fatalf("last error = %q", got.LastError)
	}
	if r.host.count("remove") != 0 {
		t.Fatalf("the recreated container was removed: %v", r.host.all())
	}
	if slices.Contains(r.admin.stmts(), `GRANT CONNECT ON DATABASE "app" TO "reader"`) {
		t.Fatal("access was given on a database whose restore failed")
	}
}

// recreatingMarker is where a running Recreate says so on disk.
func (r *recreateRig) recreatingMarker() string {
	return filepath.Join(filepath.Dir(r.backupsDir(r.d.ID)), "recreating")
}

// A Recreate that fails before any restore starts removes the container and
// the volume it made, so the Database is volume_missing again, the reason
// stays on the page, and Recreate can run again with the stored passwords.
func TestRecreateThatFailsBeforeRestoringCanRunAgain(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	eng, _ := Lookup("postgres", "18")
	r.host.mu.Lock()
	r.host.health[eng.Image("18")] = "starting"
	r.host.mu.Unlock()
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()

	if rs := r.lastRestore(t, r.d.ID); rs.Status != JobFailed {
		t.Fatalf("restore = %+v", rs)
	}
	if err := r.svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	v := r.view(t, r.d.ID)
	if v.Status != StatusVolumeMissing || !strings.Contains(v.LastError, "Recreate") {
		t.Fatalf("status = %s, last error = %q", v.Status, v.LastError)
	}
	if _, err := os.Stat(r.recreatingMarker()); !os.IsNotExist(err) {
		t.Fatalf("marker left behind: %v", err)
	}
	users, _ := r.store.ListUsers(ctx, r.d.ID)
	for _, u := range users {
		if u.Password != "pw-"+u.Name {
			t.Fatalf("stored password of %s changed", u.Name)
		}
	}

	r.host.mu.Lock()
	r.host.health[eng.Image("18")] = "healthy"
	r.host.mu.Unlock()
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatalf("second Recreate: %v", err)
	}
	r.svc.Wait()
	got, _ := r.store.Get(ctx, r.d.ID)
	if got.LastError != "" {
		t.Fatalf("last error = %q", got.LastError)
	}
	if !slices.Contains(r.admin.stmts(), `CREATE ROLE "app_owner" LOGIN PASSWORD 'pw-app_owner'`) {
		t.Fatalf("admin statements = %v", r.admin.stmts())
	}
}

// The marker is on disk while the container is made, and gone after a
// Recreate that ran to its end.
func TestRecreateMarksItselfUntilItEnds(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	marked := false
	r.host.mu.Lock()
	r.host.onCreate = func() {
		_, err := os.Stat(r.recreatingMarker())
		marked = err == nil
	}
	r.host.mu.Unlock()
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if !marked {
		t.Fatal("no marker while the container was made")
	}
	if _, err := os.Stat(r.recreatingMarker()); !os.IsNotExist(err) {
		t.Fatalf("marker left behind: %v", err)
	}
}

// A panel that stopped during a Recreate leaves its marker: the next start
// removes the half-made container and volume, before reconcile can start
// them, and says why.
func TestStartupRemovesAnInterruptedRecreate(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	// As if the panel had stopped before the job ended.
	if err := os.WriteFile(r.recreatingMarker(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.host.reset()

	if err := r.svc.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create") != 0 {
		t.Fatalf("host calls = %v", r.host.all())
	}
	v := r.view(t, r.d.ID)
	if v.Status != StatusVolumeMissing || !strings.Contains(v.LastError, "did not finish") {
		t.Fatalf("status = %s, last error = %q", v.Status, v.LastError)
	}
	if _, err := os.Stat(r.recreatingMarker()); !os.IsNotExist(err) {
		t.Fatalf("marker left behind: %v", err)
	}
}

// A Backup taken before Backups kept their database's state comes back
// owned by postgres with no user's access: the last error says so, with the
// fix, and so does the Restore's log.
func TestRecreateFromABackupWithoutStateSaysSo(t *testing.T) {
	r := newRecreateRig(t)
	ctx := context.Background()
	if err := r.store.SetBackupState(ctx, r.b.ID, BackupState{}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Recreate(ctx, r.d.ID, "Shop", []int64{r.b.ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got, _ := r.store.Get(ctx, r.d.ID)
	for _, want := range []string{"Recreated, but", "app", `ALTER DATABASE "app" OWNER TO`, "Users tab"} {
		if !strings.Contains(got.LastError, want) {
			t.Fatalf("last error lacks %q: %q", want, got.LastError)
		}
	}
	if rs := r.lastRestore(t, r.d.ID); rs.Status != JobSucceeded || !strings.Contains(rs.Log, "postgres owns app") {
		t.Fatalf("restore = %+v", rs)
	}
}
