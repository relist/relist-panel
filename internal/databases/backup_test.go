package databases

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func (r *rig) backupsDir(id int64) string {
	return filepath.Join(r.dataDir, ".databases", "1", "backups")
}

// dumps scripts every command that starts with prefix to write out and exit 0.
func (r *rig) dumps(prefix, out string) {
	r.host.script(prefix, func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, out)
		return 0, err
	})
}

func gunzipFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *rig) lastBackup(t *testing.T, id int64) Backup {
	t.Helper()
	list, err := r.store.ListBackups(context.Background(), id)
	if err != nil || len(list) == 0 {
		t.Fatalf("no backups: %v", err)
	}
	return list[0]
}

// manualBackup backs up database "app" and returns its succeeded Backup.
func (r *rig) manualBackup(t *testing.T, id int64) Backup {
	t.Helper()
	if _, err := r.svc.StartBackup(context.Background(), id, BackupRequest{Databases: []string{"app"}}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	b := r.lastBackup(t, id)
	if b.Status != JobSucceeded {
		t.Fatalf("backup = %+v", b)
	}
	return b
}

func TestBackupWritesGzipAndHash(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	rows, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != JobRunning || rows[0].Trigger != TriggerManual || rows[0].LogicalName != "app" {
		t.Fatalf("returned = %+v", rows)
	}
	r.svc.Wait()
	got := r.lastBackup(t, d.ID)
	if got.Status != JobSucceeded || got.Size <= 0 {
		t.Fatalf("backup = %+v", got)
	}
	if !regexp.MustCompile(`^\d{8}T\d{6}Z-1\.sql\.gz$`).MatchString(got.FileName) {
		t.Fatalf("file name = %q", got.FileName)
	}
	path := filepath.Join(r.backupsDir(d.ID), got.FileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got.SHA256 != hex.EncodeToString(sum[:]) || got.Size != int64(len(raw)) {
		t.Fatalf("sha %s size %d, file sha %x size %d", got.SHA256, got.Size, sum, len(raw))
	}
	if gunzipFile(t, path) != "SELECT 1;\n" {
		t.Fatal("gunzip differs")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(r.backupsDir(d.ID)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", fi.Mode().Perm())
	}
	req := r.host.execs("pg_dump -U postgres")
	if len(req) != 1 || len(req[0].Env) != 2 || req[0].Env[0] != "PGPASSWORD="+d.Password || req[0].Env[1] != "PGDATABASE=app" {
		t.Fatalf("exec env = %+v", req)
	}
	for _, a := range req[0].Cmd {
		if strings.Contains(a, d.Password) {
			t.Fatalf("password in argv: %v", req[0].Cmd)
		}
	}
}

// pgDB is the database a pg_dump or psql exec targets, from its env.
func pgDB(req ExecRequest) string {
	for _, e := range req.Env {
		if v, ok := strings.CutPrefix(e, "PGDATABASE="); ok {
			return v
		}
	}
	return ""
}

// pgDumps scripts pg_dump to write "-- <database>\n".
func (r *rig) pgDumps() {
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, "-- "+pgDB(req)+"\n")
		return 0, err
	})
}

func (r *rig) pgList(names string) { r.dumps("psql -U postgres -At -c SELECT datname", names) }

func TestBackupAllMakesOneFilePerDatabase(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.pgList("app\npostgres\n")
	r.pgDumps()
	rows, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true})
	if err != nil || len(rows) != 2 || rows[0].LogicalName != "app" || rows[1].LogicalName != "postgres" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	r.svc.Wait()
	list, _ := r.store.ListBackups(context.Background(), d.ID)
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	for _, b := range list {
		if b.Status != JobSucceeded {
			t.Fatalf("backup = %+v", b)
		}
		if got := gunzipFile(t, filepath.Join(r.backupsDir(d.ID), b.FileName)); got != "-- "+b.LogicalName+"\n" {
			t.Fatalf("%s holds %q", b.LogicalName, got)
		}
	}
	if entries, _ := os.ReadDir(r.backupsDir(d.ID)); len(entries) != 2 {
		t.Fatalf("files = %v", entries)
	}
	for i, req := range r.host.execs("pg_dump -U postgres") {
		if want := []string{"app", "postgres"}[i]; pgDB(req) != want {
			t.Fatalf("exec %d env = %v, want PGDATABASE=%s", i, req.Env, want)
		}
	}
}

func TestBackupSelectedRunsOnlyThoseDatabases(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.pgList("a\nb\nc\n")
	r.pgDumps()
	rows, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{Databases: []string{"c", "a"}})
	if err != nil || len(rows) != 2 || rows[0].LogicalName != "a" || rows[1].LogicalName != "c" {
		t.Fatalf("rows = %+v, %v (names are sorted)", rows, err)
	}
	r.svc.Wait()
	if n := r.host.count("exec relist-db-1 pg_dump "); n != 2 {
		t.Fatalf("%d dumps", n)
	}
	for _, bad := range []BackupRequest{{}, {Databases: []string{}}} {
		if _, err := r.svc.StartBackup(context.Background(), d.ID, bad); !errors.Is(err, ErrNoScope) {
			t.Fatalf("%+v: err = %v, want ErrNoScope", bad, err)
		}
	}
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{Databases: []string{"-x"}}); !errors.Is(err, ErrUnsafeName) {
		t.Fatalf("unsafe: %v", err)
	}
}

func TestBackupOneFailureKeepsOthers(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.pgList("a\nb\n")
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		if pgDB(req) == "a" {
			_, _ = io.WriteString(req.Stderr, "pg_dump: boom\n")
			return 1, nil
		}
		_, err := io.WriteString(req.Stdout, "B;\n")
		return 0, err
	})
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	list, _ := r.store.ListBackups(context.Background(), d.ID)
	byName := map[string]Backup{}
	for _, b := range list {
		byName[b.LogicalName] = b
	}
	if byName["a"].Status != JobFailed || !strings.Contains(byName["a"].Log, "boom") || byName["b"].Status != JobSucceeded {
		t.Fatalf("backups = %+v", list)
	}
	entries, _ := os.ReadDir(r.backupsDir(d.ID))
	if len(entries) != 1 || entries[0].Name() != byName["b"].FileName {
		t.Fatalf("files = %v", entries)
	}
}

func TestBackupNothingToBackUp(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	r.dumps("mysql -uroot -N -e SHOW DATABASES", "information_schema\nmysql\nperformance_schema\nsys\n")
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); !errors.Is(err, ErrNothingToBackUp) {
		t.Fatalf("err = %v", err)
	}
	if list, _ := r.store.ListBackups(context.Background(), d.ID); len(list) != 0 {
		t.Fatalf("a row was inserted: %+v", list)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot still taken")
	}
}

// Review Focus 1: a dump that writes output and then exits non-zero must fail
// the Backup and leave no file that looks like a good one.
func TestBackupFailsOnNonZeroExit(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, _ = io.WriteString(req.Stdout, "SELECT 1;\n")
		_, _ = io.WriteString(req.Stderr, "pg_dumpall: error: connection lost\n")
		return 1, nil
	})
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastBackup(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Log, "connection lost") || got.Error == "" {
		t.Fatalf("backup = %+v", got)
	}
	entries, _ := os.ReadDir(r.backupsDir(d.ID))
	if len(entries) != 0 {
		t.Fatalf("files left behind: %v", entries)
	}
}

func TestBackupFailsWhenExecErrors(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("pg_dump -U postgres", func(ExecRequest) (int, error) { return 0, errors.New("docker: gone") })
	_, _ = r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true})
	r.svc.Wait()
	got := r.lastBackup(t, d.ID)
	entries, _ := os.ReadDir(r.backupsDir(d.ID))
	if got.Status != JobFailed || !strings.Contains(got.Error, "docker: gone") || len(entries) != 0 {
		t.Fatalf("backup = %+v, files %v", got, entries)
	}
}

func TestBackupUnknownLogical(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.pgList("a\n")
	_, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{Databases: []string{"nope"}})
	var unknown ErrUnknownLogical
	if !errors.As(err, &unknown) || len(unknown.Names) != 1 || unknown.Names[0] != "nope" {
		t.Fatalf("err = %v", err)
	}
	if list, _ := r.store.ListBackups(context.Background(), d.ID); len(list) != 0 {
		t.Fatalf("a row was created: %+v", list)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot still taken")
	}
}

func TestBackupBusyAndLocked(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	started, release := make(chan struct{}), make(chan struct{})
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		close(started)
		<-release
		return 0, nil
	})
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second = %v, want ErrBusy", err)
	}
	if r.svc.Jobs(d.ID) != "backup" {
		t.Fatalf("Jobs = %q", r.svc.Jobs(d.ID))
	}
	close(release)
	r.svc.Wait()
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
	r.updateLocked = true
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); !errors.Is(err, ErrLocked) {
		t.Fatalf("update lock = %v", err)
	}
	r.updateLocked, r.locked = false, true
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); !errors.Is(err, ErrLocked) {
		t.Fatalf("migration lock = %v", err)
	}
}

func TestBackupNotRunning(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
	if list, _ := r.store.ListBackups(context.Background(), d.ID); len(list) != 0 {
		t.Fatal("a row was created")
	}
}

func TestBackupRedis400(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Cache", "redis", "8")
	if _, err := r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true}); !errors.Is(err, ErrNoBackups) {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.svc.ListLogicalDatabases(context.Background(), d.ID); !errors.Is(err, ErrNoBackups) {
		t.Fatalf("list = %v", err)
	}
}

func TestBackupLogKeepsTail(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, _ = req.Stderr.Write(bytes.Repeat([]byte("x"), 100*1024))
		_, _ = io.WriteString(req.Stderr, "THE-END")
		return 0, nil
	})
	_, _ = r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true})
	r.svc.Wait()
	got := r.lastBackup(t, d.ID)
	if len(got.Log) != 64*1024 || !strings.HasSuffix(got.Log, "THE-END") {
		t.Fatalf("log is %d bytes, ends %q", len(got.Log), got.Log[len(got.Log)-10:])
	}
}

func TestListLogicalDatabasesHidesSystemOnes(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	r.dumps("mysql -uroot -N", "information_schema\napp\nmysql\nperformance_schema\nsys\nother\n")
	got, err := r.svc.ListLogicalDatabases(context.Background(), d.ID)
	if err != nil || strings.Join(got, ",") != "app,other" {
		t.Fatalf("got %v, %v", got, err)
	}
}

// Review Focus 2: the panel restarted mid-job.
func TestStartupFailsInterruptedJobs(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	ctx := context.Background()
	b, _ := r.store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, LogicalName: "app"})
	rs, _ := r.store.InsertRestore(ctx, Restore{DatabaseID: d.ID, Source: "upload", UploadName: "x.sql"})
	dir := r.backupsDir(d.ID)
	_ = os.MkdirAll(dir, 0o700)
	partial := filepath.Join(dir, "20260101T000000Z-1.sql.gz.partial")
	_ = os.WriteFile(partial, []byte("half"), 0o600)
	upDir := filepath.Join(r.dataDir, ".databases", "1", "uploads")
	_ = os.MkdirAll(upDir, 0o700)
	upload := filepath.Join(upDir, "abc.upload")
	_ = os.WriteFile(upload, []byte("sql"), 0o600)
	good := filepath.Join(dir, "20260101T000000Z-9.sql.gz")
	_ = os.WriteFile(good, []byte("keep"), 0o600)

	if err := r.svc.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.GetBackup(ctx, d.ID, b.ID)
	if got.Status != JobFailed || got.Error != "the panel stopped during this run" {
		t.Fatalf("backup = %+v", got)
	}
	rl, _ := r.store.ListRestores(ctx, d.ID)
	if len(rl) != 1 || rl[0].ID != rs.ID || rl[0].Status != JobFailed {
		t.Fatalf("restores = %+v", rl)
	}
	for _, p := range []string{partial, upload} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s was not removed", p)
		}
	}
	if _, err := os.Stat(good); err != nil {
		t.Fatal("a finished backup file was removed")
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("the Database stayed busy")
	}
}

func TestWaitIdleWaitsForJob(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	started, release := make(chan struct{}), make(chan struct{})
	r.host.script("pg_dump -U postgres", func(ExecRequest) (int, error) { close(started); <-release; return 0, nil })
	_, _ = r.svc.StartBackup(context.Background(), d.ID, BackupRequest{All: true})
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.svc.WaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitIdle with a running job = %v, want the ctx error", err)
	}
	done := make(chan error, 1)
	go func() { done <- r.svc.WaitIdle(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("WaitIdle returned %v before the job ended", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitIdle did not return after the job ended")
	}
	if err := r.svc.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBackupPathAndDelete(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	_, _ = r.svc.StartBackup(ctx, d.ID, BackupRequest{All: true})
	r.svc.Wait()
	b := r.lastBackup(t, d.ID)
	path, _, err := r.svc.BackupPath(ctx, d.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.BackupPath(ctx, d.ID+1, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other database: %v", err)
	}
	if err := r.svc.DeleteBackup(ctx, d.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file survived")
	}
	if _, err := r.store.GetBackup(ctx, d.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("row survived")
	}
	run, _ := r.store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, LogicalName: "app"})
	if err := r.svc.DeleteBackup(ctx, d.ID, run.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("running: %v", err)
	}
}
