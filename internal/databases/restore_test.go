package databases

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/agefile"
)

const (
	pgAdmin   = "psql -U postgres -X -At -v ON_ERROR_STOP=1 -d template1"
	pgRestore = "psql -U postgres -X -q -v ON_ERROR_STOP=1"
	pgList    = "psql -U postgres -At -c SELECT datname"
)

// adminSQL scripts the admin command: an inspect answers owner, and every
// stdin is kept in order. hook, when set, runs for each one.
func (r *rig) adminSQL(prefix, owner string, hook func(sql string)) *[]string {
	var got []string
	r.host.script(prefix, func(req ExecRequest) (int, error) {
		if req.Stdin == nil { // the list of databases, which a mysql prefix also matches
			_, err := io.WriteString(req.Stdout, "app\n")
			return 0, err
		}
		b, _ := io.ReadAll(req.Stdin)
		sql := string(b)
		got = append(got, sql)
		if hook != nil {
			hook(sql)
		}
		if strings.Contains(sql, "pg_get_userbyid") || strings.Contains(sql, "SCHEMATA") {
			_, _ = io.WriteString(req.Stdout, owner)
		}
		return 0, nil
	})
	return &got
}

// captureRestore scripts the restore command to keep its stdin.
func (r *rig) captureRestore(prefix, stderr string) *[]string {
	var got []string
	r.host.script(prefix, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		got = append(got, string(b))
		_, _ = io.WriteString(req.Stderr, stderr)
		return 0, nil
	})
	return &got
}

func (r *rig) lastRestore(t *testing.T, id int64) Restore {
	t.Helper()
	list, err := r.store.ListRestores(context.Background(), id)
	if err != nil || len(list) == 0 {
		t.Fatalf("no restores: %v", err)
	}
	return list[0]
}

// execLines are the recorded execs, without the read-only list of databases.
func execLines(r *rig) []string {
	var out []string
	for _, c := range r.host.all() {
		if strings.HasPrefix(c, "exec ") && !strings.Contains(c, "SELECT datname") && !strings.Contains(c, "SHOW DATABASES") {
			out = append(out, c)
		}
	}
	return out
}

func uploadsLeft(r *rig) []os.DirEntry {
	left, _ := os.ReadDir(filepath.Join(r.dataDir, ".databases", "1", "uploads"))
	return left
}

func gz(s string) []byte {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return b.Bytes()
}

func TestRestoreFromBackupReplacesTarget(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID)
	admin := r.adminSQL(pgAdmin, "postgres\n", nil)
	stdin := r.captureRestore(pgRestore, "")
	r.host.reset()

	rs, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop", false)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Status != JobRunning || rs.Source != "backup" || rs.BackupID == nil || *rs.BackupID != b.ID || rs.Target != "app" {
		t.Fatalf("returned = %+v", rs)
	}
	r.svc.Wait()
	// The pre_restore Backup reads its state and dumps; then the restore's
	// inspect, the per-database settings, the replace and the restore.
	lines := execLines(r)
	if len(lines) != 6 || !strings.HasSuffix(lines[0], pgAdmin) || !strings.Contains(lines[1], "pg_dump") || !strings.HasSuffix(lines[2], pgAdmin) ||
		!strings.HasSuffix(lines[3], pgAdmin) || !strings.HasSuffix(lines[4], pgAdmin) || !strings.HasSuffix(lines[5], pgRestore) {
		t.Fatalf("exec order = %v", lines)
	}
	if len(*admin) != 4 || !strings.HasPrefix((*admin)[0], "SELECT pg_get_userbyid") || !strings.HasPrefix((*admin)[1], "SELECT pg_get_userbyid") ||
		!strings.Contains((*admin)[2], "pg_db_role_setting") || !strings.HasPrefix((*admin)[3], `DROP DATABASE IF EXISTS "app" WITH (FORCE);`) {
		t.Fatalf("admin sql = %q", *admin)
	}
	if len(*stdin) != 1 || (*stdin)[0] != "ORIGINAL;\n" {
		t.Fatalf("restore stdin = %q", *stdin)
	}
	got := r.lastRestore(t, d.ID)
	if got.Status != JobSucceeded || got.PreBackupID == nil || got.Target != "app" {
		t.Fatalf("restore = %+v", got)
	}
	pre, err := r.store.GetBackup(ctx, d.ID, *got.PreBackupID)
	if err != nil || pre.Trigger != TriggerPreRestore || pre.LogicalName != "app" || pre.Status != JobSucceeded {
		t.Fatalf("pre = %+v, %v", pre, err)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

// A Backup restored under another name copies the database: no pre_restore.
func TestRestoreFromBackupIntoNewTarget(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID)
	r.adminSQL(pgAdmin, "", nil)
	r.captureRestore(pgRestore, "")
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app_copy", "Shop", false); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobSucceeded || got.PreBackupID != nil || got.Target != "app_copy" {
		t.Fatalf("restore = %+v", got)
	}
	if bl, _ := r.store.ListBackups(ctx, d.ID); len(bl) != 1 {
		t.Fatalf("a pre_restore Backup was taken: %+v", bl)
	}
}

// Review Focus 1: an upload runs in the target the Admin picked, never in
// the database the connection happens to start in.
func TestRestoreUploadRunsInTarget(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	admin := r.adminSQL(pgAdmin, "", nil)
	stdin := r.captureRestore(pgRestore, "")
	r.host.reset()

	rs, err := r.svc.RestoreFromUpload(ctx, d.ID, "shop", "Shop", "plain.sql", "", strings.NewReader("CREATE TABLE t (id int);\n"))
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	lines := execLines(r)
	if len(lines) != 3 || !strings.HasSuffix(lines[0], pgAdmin) || !strings.HasSuffix(lines[1], pgAdmin) || !strings.HasSuffix(lines[2], pgRestore) {
		t.Fatalf("exec order = %v", lines)
	}
	reqs := r.host.execs(pgRestore)
	if len(reqs) != 1 || pgDB(reqs[0]) != "shop" || reqs[0].Env[0] != "PGPASSWORD="+d.Password {
		t.Fatalf("restore env = %+v", reqs)
	}
	if len(*admin) != 2 || !strings.Contains((*admin)[1], `CREATE DATABASE "shop" TEMPLATE template0 OWNER "postgres"`) {
		t.Fatalf("admin = %q", *admin)
	}
	if len(*stdin) != 1 || (*stdin)[0] != "CREATE TABLE t (id int);\n" {
		t.Fatalf("stdin = %q", *stdin)
	}
	if bl, _ := r.store.ListBackups(ctx, d.ID); len(bl) != 0 {
		t.Fatalf("a Backup was taken for a new target: %+v", bl)
	}
	got := r.lastRestore(t, d.ID)
	if got.Status != JobSucceeded || got.Target != "shop" || got.ID != rs.ID || rs.Target != "shop" {
		t.Fatalf("restore = %+v", got)
	}
}

// Review Focus 2: a whole-instance upload is refused with its line before
// any exec, Backup row, restore row or file.
func TestRestoreUploadRefusedBeforeAnything(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.host.reset()
	_, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "Shop", "all.sql", "", strings.NewReader("CREATE ROLE x;\n"))
	var f ErrForbiddenStatement
	if !errors.As(err, &f) || f.Line != 1 {
		t.Fatalf("err = %v, want ErrForbiddenStatement at line 1", err)
	}
	if lines := execLines(r); len(lines) != 0 {
		t.Fatalf("execs ran: %v", lines)
	}
	if bl, _ := r.store.ListBackups(ctx, d.ID); len(bl) != 0 {
		t.Fatalf("Backup rows: %+v", bl)
	}
	if rl, _ := r.store.ListRestores(ctx, d.ID); len(rl) != 0 {
		t.Fatalf("restore rows: %+v", rl)
	}
	if left := uploadsLeft(r); len(left) != 0 {
		t.Fatalf("upload files left: %v", left)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

// Review Focus 4: a replaced Postgres database keeps its owner.
func TestReplaceKeepsOwner(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "PRE;\n")
	preFirst := false
	admin := r.adminSQL(pgAdmin, "appuser\n", func(sql string) {
		if strings.HasPrefix(sql, "DROP DATABASE") {
			bl, _ := r.store.ListBackups(ctx, d.ID)
			preFirst = len(bl) == 1 && bl[0].Trigger == TriggerPreRestore && bl[0].LogicalName == "app" && bl[0].Status == JobSucceeded
		}
	})
	r.captureRestore(pgRestore, "")
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "Shop", "a.sql", "", strings.NewReader("SELECT 1;\n")); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	want := "DROP DATABASE IF EXISTS \"app\" WITH (FORCE);\nCREATE DATABASE \"app\" TEMPLATE template0 OWNER \"appuser\";\n"
	// The pre_restore Backup's state, the inspect, the settings, the replace.
	if len(*admin) != 4 || (*admin)[3] != want {
		t.Fatalf("replace sql = %q", *admin)
	}
	if !preFirst {
		t.Fatal("the pre_restore Backup of app did not exist before the replace")
	}
}

func TestRestoreMySQLKeepsCharset(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	admin := r.adminSQL("mysql -uroot -N", "utf8mb4\tutf8mb4_bin\n", nil)
	stdin := r.captureRestore("mysql -uroot --binary-mode --database=app", "")
	if _, err := r.svc.RestoreFromUpload(context.Background(), d.ID, "app", "Shop", "a.sql", "", strings.NewReader("CREATE TABLE t (id int);")); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	// The pre_restore Backup's state, the inspect, the replace.
	if len(*admin) != 3 || (*admin)[2] != "DROP DATABASE IF EXISTS `app`;\nCREATE DATABASE `app` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;\n" {
		t.Fatalf("admin = %q", *admin)
	}
	reqs := r.host.execs("mysql -uroot --binary-mode --database=app")
	if len(reqs) != 1 || len(reqs[0].Env) != 1 || reqs[0].Env[0] != "MYSQL_PWD="+d.Password || len(*stdin) != 1 {
		t.Fatalf("restore execs = %+v", reqs)
	}
	if got := r.lastRestore(t, d.ID); got.Status != JobSucceeded {
		t.Fatalf("restore = %+v", got)
	}
}

// Review Focus (phase 2) 4: when the pre_restore Backup fails, nothing runs.
func TestRestoreAbortsWhenPreBackupFails(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	failing := false
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, _ = io.WriteString(req.Stdout, "ORIGINAL;\n")
		if failing {
			return 1, nil
		}
		return 0, nil
	})
	b := r.manualBackup(t, d.ID)
	r.host.reset()
	failing = true

	if _, err := r.svc.RestoreFromBackup(context.Background(), d.ID, b.ID, "app", "Shop", false); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Error, "pre-restore backup failed") {
		t.Fatalf("restore = %+v", got)
	}
	// The Backup reads its state before its dump; nothing runs after the dump.
	dumped := false
	for _, l := range execLines(r) {
		if dumped && strings.Contains(l, "psql") {
			t.Fatalf("a psql exec ran after the pre-restore backup failed: %v", execLines(r))
		}
		dumped = dumped || strings.Contains(l, "pg_dump")
	}
	if got.PreBackupID == nil {
		t.Fatal("the failed pre_restore Backup is not linked")
	}
}

// Review Focus (phase 2) 5: a Backup file that changed on disk is refused
// before the pre_restore Backup.
func TestRestoreRefusesChangedFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID)
	path := filepath.Join(r.backupsDir(d.ID), b.FileName)
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r.host.reset()

	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop", false); !errors.Is(err, ErrChangedFile) {
		t.Fatalf("err = %v, want ErrChangedFile", err)
	}
	if rl, _ := r.store.ListRestores(ctx, d.ID); len(rl) != 0 {
		t.Fatalf("a restore row exists: %+v", rl)
	}
	if lines := execLines(r); len(lines) != 0 {
		t.Fatalf("execs ran: %v", lines)
	}
	if bl, _ := r.store.ListBackups(ctx, d.ID); len(bl) != 1 {
		t.Fatalf("a pre_restore Backup was taken: %+v", bl)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

// Review Focus (phase 2) 3: gzip is detected by its first two bytes, never by
// the name, and the check reads the un-gzipped text.
func TestRestoreUploadDetectsGzipByContent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "PRE;\n")
	r.adminSQL(pgAdmin, "postgres\n", nil)
	stdin := r.captureRestore(pgRestore, "")

	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "Shop", "x.sql", "", bytes.NewReader(gz("SELECT 1;\n"))); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "Shop", "y.sql.gz", "", strings.NewReader("SELECT 2;\n")); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if len(*stdin) != 2 || (*stdin)[0] != "SELECT 1;\n" || (*stdin)[1] != "SELECT 2;\n" {
		t.Fatalf("restore stdin = %q", *stdin)
	}
	list, _ := r.store.ListRestores(ctx, d.ID)
	if len(list) != 2 || list[0].Status != JobSucceeded || list[0].Source != "upload" || list[0].UploadName != "y.sql.gz" || list[0].BackupID != nil {
		t.Fatalf("restores = %+v", list)
	}
	if left := uploadsLeft(r); len(left) != 0 {
		t.Fatalf("upload files left: %v", left)
	}
	r.host.reset()
	var f ErrForbiddenStatement
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "Shop", "z.sql", "", bytes.NewReader(gz("\\connect x\n"))); !errors.As(err, &f) || f.Line != 1 {
		t.Fatalf("gzipped \\connect: err = %v", err)
	}
	if len(execLines(r)) != 0 || len(uploadsLeft(r)) != 0 {
		t.Fatalf("something ran: %v", execLines(r))
	}
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "Shop", "bad.gz", "", strings.NewReader("\x1f\x8bnot gzip")); !errors.Is(err, ErrBadUpload) {
		t.Fatalf("broken gzip: err = %v", err)
	}
}

func TestRestoreRefusesOldFormat(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	b, _ := r.store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual})
	dir := r.backupsDir(d.ID)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "old.sql.gz"), []byte("x"), 0o600)
	_ = r.store.FinishBackup(ctx, b.ID, Backup{Status: JobSucceeded, FileName: "old.sql.gz", SHA256: "x"})
	r.host.reset()
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop", false); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("err = %v", err)
	}
	if len(execLines(r)) != 0 || r.svc.Jobs(d.ID) != "" {
		t.Fatal("something started")
	}
}

func TestRestoreStopsAtFirstError(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.adminSQL(pgAdmin, "", nil)
	r.host.script(pgRestore, func(req ExecRequest) (int, error) {
		_, _ = io.Copy(io.Discard, req.Stdin)
		_, _ = io.WriteString(req.Stderr, "psql:<stdin>:12: ERROR:  syntax error")
		return 3, nil
	})
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "fresh", "Shop", "a.sql", "", strings.NewReader("x;")); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || got.Error != "the restore command exited with code 3" || !strings.Contains(got.Log, "syntax error") {
		t.Fatalf("restore = %+v", got)
	}
}

func TestRestoreFailsWhenReplaceFails(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		if strings.HasPrefix(string(b), "DROP") {
			_, _ = io.WriteString(req.Stderr, "ERROR:  database \"fresh\" is being accessed")
			return 3, nil
		}
		return 0, nil
	})
	r.captureRestore(pgRestore, "")
	_, _ = r.svc.RestoreFromUpload(context.Background(), d.ID, "fresh", "Shop", "a.sql", "", strings.NewReader("x;"))
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Log, "is being accessed") || r.host.count("exec relist-db-1 "+pgRestore) != 0 {
		t.Fatalf("restore = %+v", got)
	}
}

func TestRestoreWrongConfirmAndTarget(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	b := r.manualBackup(t, d.ID)
	r.host.reset()
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "shop", false); !errors.Is(err, ErrConfirm) {
		t.Fatalf("backup: %v", err)
	}
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "nope", "a.sql", "", strings.NewReader("x")); !errors.Is(err, ErrConfirm) {
		t.Fatalf("upload: %v", err)
	}
	for _, bad := range []string{"", "template1", "-x", "a b"} {
		if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, bad, "Shop", false); !errors.Is(err, ErrBadTarget) {
			t.Fatalf("target %q: %v", bad, err)
		}
		if _, err := r.svc.RestoreFromUpload(ctx, d.ID, bad, "Shop", "a.sql", "", strings.NewReader("x")); !errors.Is(err, ErrBadTarget) {
			t.Fatalf("upload target %q: %v", bad, err)
		}
	}
	if rl, _ := r.store.ListRestores(ctx, d.ID); len(rl) != 0 || len(execLines(r)) != 0 || r.svc.Jobs(d.ID) != "" || len(uploadsLeft(r)) != 0 {
		t.Fatalf("something started: %+v %v", rl, execLines(r))
	}
}

func TestRestoreOfFailedBackupRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	b, _ := r.store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, LogicalName: "app"})
	_ = r.store.FinishBackup(ctx, b.ID, Backup{Status: JobFailed, Error: "x"})
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

const agePass = "restore-passphrase-0123456789"

func ageBytes(t *testing.T, pass, plain string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := agefile.Encrypt(&buf, pass, strings.NewReader(plain)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadsDirEmpty(t *testing.T, r *rig, id int64) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.svc.dbDir(id), "uploads"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("uploads dir not empty: %v", entries)
	}
}

func TestRestoreEncryptedUpload(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.adminSQL(pgAdmin, "", nil)
	stdin := r.captureRestore(pgRestore, "")
	r.host.reset()
	_, err := r.svc.RestoreFromUpload(ctx, d.ID, "shop", "Shop", "p.sql.age", agePass,
		bytes.NewReader(ageBytes(t, agePass, "CREATE TABLE t (id int);\n")))
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if len(*stdin) != 1 || (*stdin)[0] != "CREATE TABLE t (id int);\n" {
		t.Fatalf("stdin = %q", *stdin)
	}
	if got := r.lastRestore(t, d.ID); got.Status != JobSucceeded {
		t.Fatalf("restore = %+v", got)
	}
	uploadsDirEmpty(t, r, d.ID)
}

// Review Focus 4: a wrong passphrase answers cleanly and leaves no temp file.
func TestRestoreEncryptedUploadWrongPassphrase(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.adminSQL(pgAdmin, "", nil)
	r.host.reset()
	_, err := r.svc.RestoreFromUpload(ctx, d.ID, "shop", "Shop", "p.sql.age", "not-the-passphrase-00000000",
		bytes.NewReader(ageBytes(t, agePass, "SELECT 1;\n")))
	if !errors.Is(err, agefile.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
	uploadsDirEmpty(t, r, d.ID)
}

func TestRestoreEncryptedUploadNeedsPassphrase(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.adminSQL(pgAdmin, "", nil)
	r.host.reset()
	_, err := r.svc.RestoreFromUpload(ctx, d.ID, "shop", "Shop", "p.sql.age", "",
		bytes.NewReader(ageBytes(t, agePass, "SELECT 1;\n")))
	if !errors.Is(err, ErrNeedPassphrase) {
		t.Fatalf("err = %v, want ErrNeedPassphrase", err)
	}
	uploadsDirEmpty(t, r, d.ID)
}

func TestRestoreTooExpensiveUploadRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.adminSQL(pgAdmin, "", nil)
	r.host.reset()
	enc := bytes.Replace(ageBytes(t, agePass, "SELECT 1;\n"), []byte(" 15"), []byte(" 22"), 1)
	_, err := r.svc.RestoreFromUpload(ctx, d.ID, "shop", "Shop", "p.sql.age", agePass, bytes.NewReader(enc))
	if !errors.Is(err, agefile.ErrTooExpensive) {
		t.Fatalf("err = %v, want ErrTooExpensive", err)
	}
	uploadsDirEmpty(t, r, d.ID)
}

func TestRestoreDamagedEncryptedUpload(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.adminSQL(pgAdmin, "", nil)
	r.host.reset()
	enc := ageBytes(t, agePass, "SELECT 1;\n")
	enc[len(enc)-1] ^= 0xff
	_, err := r.svc.RestoreFromUpload(ctx, d.ID, "shop", "Shop", "p.sql.age", agePass, bytes.NewReader(enc))
	if !errors.Is(err, agefile.ErrDamaged) {
		t.Fatalf("err = %v, want ErrDamaged", err)
	}
	uploadsDirEmpty(t, r, d.ID)
}

// roleAdmin scripts the Postgres admin command for a Backup and a restore:
// an inspect prints prev, a superuser check prints super ("t", "f", or ""
// for a role that does not exist), and every stdin is kept in order.
func (r *rig) roleAdmin(prev, super string) *[]string {
	var got []string
	r.host.script(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		sql := string(b)
		got = append(got, sql)
		switch {
		case strings.Contains(sql, "rolsuper"):
			_, _ = io.WriteString(req.Stdout, super)
		case strings.Contains(sql, "pg_get_userbyid") && strings.Contains(sql, "datname = 'app';"):
			_, _ = io.WriteString(req.Stdout, prev)
		}
		return 0, nil
	})
	return &got
}

// A Backup keeps what its dump leaves out (review 2026-11-20): restored into
// a new database "as it was backed up", the database gets its owner back,
// still restricted like Create database, so the app's user can use it.
func TestRestoreIntoNewDatabaseAsBackedUp(t *testing.T) {
	for _, c := range []struct {
		name, super string
		asBackedUp  bool
		owner, note string
	}{
		{"owner back", "f\n", true, "app", ""},
		{"not asked", "f\n", false, "postgres", ""},
		{"owner gone", "", true, "postgres", "app no longer exists"},
		{"owner now a superuser", "t\n", true, "postgres", "app is a superuser now"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			ctx := context.Background()
			d := r.create(t, "Shop", "postgres", "18")
			r.dumps("pg_dump -U postgres", "ORIGINAL;\n")
			admin := r.roleAdmin("app\t{app=CTc/app,reader=c/app}\n", c.super)
			b := r.manualBackup(t, d.ID)
			if b.State.Prev != "app\t{app=CTc/app,reader=c/app}" {
				t.Fatalf("state = %+v, want the inspect line", b.State)
			}
			r.captureRestore(pgRestore, "")
			if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app_copy", "Shop", c.asBackedUp); err != nil {
				t.Fatal(err)
			}
			r.svc.Wait()
			got := r.lastRestore(t, d.ID)
			if got.Status != JobSucceeded || !strings.Contains(got.Log, c.note) {
				t.Fatalf("restore = %+v, want a log with %q", got, c.note)
			}
			want := "CREATE DATABASE \"app_copy\" TEMPLATE template0 OWNER \"" + c.owner + "\";\nREVOKE CONNECT ON DATABASE \"app_copy\" FROM PUBLIC;\n"
			if all := strings.Join(*admin, "\n"); !strings.Contains(all, want) {
				t.Fatalf("admin SQL lacks %q:\n%s", want, all)
			}
		})
	}
}

func TestRestoreMySQLIntoNewDatabaseAsBackedUp(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "mysql", "8.4")
	r.dumps("mysqldump", "CREATE TABLE t (id int);\n")
	var admin []string
	r.host.script("mysql -uroot -N", func(req ExecRequest) (int, error) {
		if req.Stdin == nil { // the list of databases
			_, err := io.WriteString(req.Stdout, "app\n")
			return 0, err
		}
		b, _ := io.ReadAll(req.Stdin)
		admin = append(admin, string(b))
		if strings.Contains(string(b), "SCHEMA_NAME = 'app';") {
			_, _ = io.WriteString(req.Stdout, "utf8mb4\tutf8mb4_bin\n")
		}
		return 0, nil
	})
	b := r.manualBackup(t, d.ID)
	if b.State.Prev != "utf8mb4\tutf8mb4_bin" {
		t.Fatalf("state = %+v", b.State)
	}
	r.captureRestore("mysql -uroot --binary-mode --database=app_copy", "")
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app_copy", "Shop", true); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if got := r.lastRestore(t, d.ID); got.Status != JobSucceeded {
		t.Fatalf("restore = %+v", got)
	}
	want := "CREATE DATABASE `app_copy` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;\n"
	if all := strings.Join(admin, "\n"); !strings.Contains(all, want) {
		t.Fatalf("admin SQL lacks %q:\n%s", want, all)
	}
}

// A Backup also keeps each login user's access to its database: Recreate
// gives it back, since a one-database dump carries no CONNECT or MySQL
// schema grant. It is read in the same admin session as the owner.
func TestBackupKeepsUsersAccess(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	var sent string
	r.host.script(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		sent = string(b)
		_, _ = io.WriteString(req.Stdout, "app\t{app=CTc/app,reader=c/app}\nreader\tread\napp\tfull\n")
		return 0, nil
	})
	b := r.manualBackup(t, d.ID)
	if b.State.Prev != "app\t{app=CTc/app,reader=c/app}" || len(b.State.Access) != 2 || b.State.Access["reader"] != AccessRead || b.State.Access["app"] != AccessFull {
		t.Fatalf("state = %+v", b.State)
	}
	if !strings.Contains(sent, "pg_get_userbyid") || !strings.Contains(sent, pgDumper{}.AccessSQL("app")) {
		t.Fatalf("one session must read both: %q", sent)
	}
	got, _ := r.store.GetBackup(context.Background(), d.ID, b.ID)
	if got.State.Access["reader"] != AccessRead {
		t.Fatalf("stored state = %+v", got.State)
	}
}

// The access query reads the same rows as the Users tab, for one database,
// as "<user>\t<level>" lines the admin command prints.
func TestAccessSQL(t *testing.T) {
	pg := pgDumper{}.AccessSQL("my-app")
	if !strings.Contains(pg, pgAccounts{}.AccessQuery()) || !strings.Contains(pg, "x.d = 'my-app'") || !strings.Contains(pg, `E'\t'`) {
		t.Errorf("postgres = %s", pg)
	}
	my := mysqlDumper{}.AccessSQL("my_app")
	if !strings.Contains(my, myAccounts{}.AccessQuery()) || !strings.Contains(my, "y.db = 'my_app'") || !strings.Contains(myAccounts{}.AccessQuery(), "END AS lvl") {
		t.Errorf("mysql = %s", my)
	}
}
