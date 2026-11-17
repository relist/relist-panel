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

	rs, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Status != JobRunning || rs.Source != "backup" || rs.BackupID == nil || *rs.BackupID != b.ID || rs.Target != "app" {
		t.Fatalf("returned = %+v", rs)
	}
	r.svc.Wait()
	lines := execLines(r)
	if len(lines) != 4 || !strings.Contains(lines[0], "pg_dump") || !strings.HasSuffix(lines[1], pgAdmin) ||
		!strings.HasSuffix(lines[2], pgAdmin) || !strings.HasSuffix(lines[3], pgRestore) {
		t.Fatalf("exec order = %v", lines)
	}
	if len(*admin) != 2 || !strings.HasPrefix((*admin)[0], "SELECT pg_get_userbyid") || !strings.HasPrefix((*admin)[1], `DROP DATABASE IF EXISTS "app" WITH (FORCE);`) {
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
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app_copy", "Shop"); err != nil {
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
	if len(*admin) != 2 || (*admin)[1] != want {
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
	if len(*admin) != 2 || (*admin)[1] != "DROP DATABASE IF EXISTS `app`;\nCREATE DATABASE `app` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;\n" {
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

	if _, err := r.svc.RestoreFromBackup(context.Background(), d.ID, b.ID, "app", "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Error, "pre-restore backup failed") {
		t.Fatalf("restore = %+v", got)
	}
	for _, l := range execLines(r) {
		if strings.Contains(l, "psql") {
			t.Fatalf("a psql exec ran after the pre-restore backup failed: %v", execLines(r))
		}
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

	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop"); !errors.Is(err, ErrChangedFile) {
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
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop"); !errors.Is(err, ErrNotRestorable) {
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
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "shop"); !errors.Is(err, ErrConfirm) {
		t.Fatalf("backup: %v", err)
	}
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "app", "nope", "a.sql", "", strings.NewReader("x")); !errors.Is(err, ErrConfirm) {
		t.Fatalf("upload: %v", err)
	}
	for _, bad := range []string{"", "template1", "-x", "a b"} {
		if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, bad, "Shop"); !errors.Is(err, ErrBadTarget) {
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
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop"); !errors.Is(err, ErrNotFound) {
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
