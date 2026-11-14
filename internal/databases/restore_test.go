//go:build phase2b_pending

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
)

// manualBackup makes a succeeded whole-instance Backup whose dump is out.
func (r *rig) manualBackup(t *testing.T, id int64, sc Scope) Backup {
	t.Helper()
	if _, err := r.svc.StartBackup(context.Background(), id, sc); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	b := r.lastBackup(t, id)
	if b.Status != JobSucceeded {
		t.Fatalf("backup = %+v", b)
	}
	return b
}

// captureRestore scripts the restore command to keep its stdin.
func (r *rig) captureRestore(prefix string, stderr string) *[]string {
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

func execLines(r *rig) []string {
	var out []string
	for _, c := range r.host.all() {
		if strings.HasPrefix(c, "exec ") {
			out = append(out, c)
		}
	}
	return out
}

func TestRestoreFromBackupRunsPreBackupThenRestore(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dumpall", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID, Scope{All: true})
	stdin := r.captureRestore("psql -U postgres -d template1 -v ON_ERROR_STOP=0", "")
	r.host.reset()

	rs, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "Shop")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Status != JobRunning || rs.Source != "backup" || rs.BackupID == nil || *rs.BackupID != b.ID {
		t.Fatalf("returned = %+v", rs)
	}
	r.svc.Wait()
	lines := execLines(r)
	if len(lines) != 3 || !strings.Contains(lines[0], "pg_dumpall") || !strings.Contains(lines[1], "pg_terminate_backend") ||
		!strings.Contains(lines[2], "ON_ERROR_STOP=0") {
		t.Fatalf("exec order = %v", lines)
	}
	if len(*stdin) != 1 || (*stdin)[0] != "ORIGINAL;\n" {
		t.Fatalf("restore stdin = %q", *stdin)
	}
	got := r.lastRestore(t, d.ID)
	if got.Status != JobSucceeded || got.PreBackupID == nil {
		t.Fatalf("restore = %+v", got)
	}
	pre, err := r.store.GetBackup(ctx, d.ID, *got.PreBackupID)
	if err != nil || pre.Trigger != TriggerPreRestore || !pre.Scope.All || pre.Status != JobSucceeded {
		t.Fatalf("pre = %+v, %v", pre, err)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

// Review Focus 4: when the pre_restore Backup fails, no restore command runs.
func TestRestoreAbortsWhenPreBackupFails(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	failing := false
	r.host.script("pg_dumpall", func(req ExecRequest) (int, error) {
		_, _ = io.WriteString(req.Stdout, "ORIGINAL;\n")
		if failing {
			return 1, nil
		}
		return 0, nil
	})
	b := r.manualBackup(t, d.ID, Scope{All: true})
	r.host.reset()
	failing = true

	if _, err := r.svc.RestoreFromBackup(context.Background(), d.ID, b.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Error, "pre-restore backup failed") {
		t.Fatalf("restore = %+v", got)
	}
	if n := r.host.count("exec relist-db-1 psql"); n != 0 {
		t.Fatalf("%d psql execs ran after the pre-restore backup failed: %v", n, execLines(r))
	}
	if got.PreBackupID == nil {
		t.Fatal("the failed pre_restore Backup is not linked")
	}
}

// Review Focus 5: a Backup file that changed on disk is refused before the
// pre_restore Backup.
func TestRestoreRefusesChangedFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dumpall", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID, Scope{All: true})
	path := filepath.Join(r.backupsDir(d.ID), b.FileName)
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r.host.reset()

	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "Shop"); !errors.Is(err, ErrChangedFile) {
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

// Review Focus 3: gzip is detected by its first two bytes, never by the name.
func TestRestoreUploadDetectsGzipByContent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dumpall", "PRE;\n")
	stdin := r.captureRestore("psql -U postgres -d template1 -v ON_ERROR_STOP=0", "")

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("SELECT 1;\n"))
	_ = zw.Close()
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "Shop", "x.sql", bytes.NewReader(gz.Bytes())); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "Shop", "y.sql.gz", strings.NewReader("SELECT 2;\n")); err != nil {
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
	left, _ := os.ReadDir(filepath.Join(r.dataDir, ".databases", "1", "uploads"))
	if len(left) != 0 {
		t.Fatalf("upload files left: %v", left)
	}
}

func TestRestoreFailsOnRealError(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dumpall", "ORIGINAL;\n")
	stderr := "psql:<stdin>:12: ERROR:  role \"postgres\" already exists\n"
	r.host.script("psql -U postgres -d template1 -v ON_ERROR_STOP=0", func(req ExecRequest) (int, error) {
		_, _ = io.Copy(io.Discard, req.Stdin)
		_, _ = io.WriteString(req.Stderr, stderr)
		return 0, nil
	})
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "Shop", "a.sql", strings.NewReader("SELECT 1;")); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if got := r.lastRestore(t, d.ID); got.Status != JobSucceeded {
		t.Fatalf("the always-expected error failed the restore: %+v", got)
	}
	stderr += "psql:<stdin>:99: ERROR:  syntax error at or near \"x\"\n"
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "Shop", "b.sql", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Log, "syntax error") || got.Error == "" {
		t.Fatalf("restore = %+v", got)
	}
}

func TestRestoreFailsOnNonZeroExit(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	r.dumps("mysqldump", "X;\n")
	r.host.script("mysql -uroot", func(req ExecRequest) (int, error) {
		_, _ = io.Copy(io.Discard, req.Stdin)
		_, _ = io.WriteString(req.Stderr, "ERROR 1064 (42000) at line 1")
		return 1, nil
	})
	_, _ = r.svc.RestoreFromUpload(context.Background(), d.ID, "Shop", "a.sql", strings.NewReader("x"))
	r.svc.Wait()
	got := r.lastRestore(t, d.ID)
	if got.Status != JobFailed || !strings.Contains(got.Log, "ERROR 1064") {
		t.Fatalf("restore = %+v", got)
	}
}

func TestRestoreSelectedTerminatesOnlyItsDatabases(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("psql -U postgres -At", "app\no'brien\nother\n")
	r.dumps("pg_dump ", "X;\n")
	r.dumps("pg_dumpall", "ALL;\n")
	b := r.manualBackup(t, d.ID, Scope{Databases: []string{"app", "o'brien"}})
	r.captureRestore("psql -U postgres -d template1 -v", "")
	r.host.reset()
	if _, err := r.svc.RestoreFromBackup(context.Background(), d.ID, b.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	var sql string
	for _, e := range r.host.execs("psql -U postgres -d template1 -c") {
		sql = e.Cmd[len(e.Cmd)-1]
	}
	if !strings.Contains(sql, "datname IN ('app', 'o''brien')") || strings.Contains(sql, "other") || strings.Contains(sql, "IS NOT NULL") {
		t.Fatalf("terminate sql = %q", sql)
	}
	// A whole-instance Backup terminates every database but template1.
	all := r.manualBackup(t, d.ID, Scope{All: true})
	r.host.reset()
	_, _ = r.svc.RestoreFromBackup(context.Background(), d.ID, all.ID, "Shop")
	r.svc.Wait()
	for _, e := range r.host.execs("psql -U postgres -d template1 -c") {
		sql = e.Cmd[len(e.Cmd)-1]
	}
	if !strings.Contains(sql, "datname IS NOT NULL AND datname <> 'template1'") || strings.Contains(sql, "IN (") {
		t.Fatalf("whole terminate sql = %q", sql)
	}
}

func TestRestoreMySQLNoTerminate(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	r.dumps("mysqldump", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID, Scope{All: true})
	stdin := r.captureRestore("mysql -uroot", "")
	r.host.reset()
	if _, err := r.svc.RestoreFromBackup(context.Background(), d.ID, b.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	lines := execLines(r)
	if len(lines) != 2 || !strings.Contains(lines[0], "mysqldump") || lines[1] != "exec relist-db-1 mysql -uroot" {
		t.Fatalf("execs = %v", lines)
	}
	reqs := r.host.execs("mysql -uroot")
	if len(reqs[0].Env) != 1 || reqs[0].Env[0] != "MYSQL_PWD="+d.Password || len(*stdin) != 1 {
		t.Fatalf("env = %v", reqs[0].Env)
	}
	if got := r.lastRestore(t, d.ID); got.Status != JobSucceeded {
		t.Fatalf("restore = %+v", got)
	}
}

func TestRestoreWrongConfirm(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dumpall", "X;\n")
	b := r.manualBackup(t, d.ID, Scope{All: true})
	r.host.reset()
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "shop"); !errors.Is(err, ErrConfirm) {
		t.Fatalf("backup: %v", err)
	}
	if _, err := r.svc.RestoreFromUpload(ctx, d.ID, "nope", "a.sql", strings.NewReader("x")); !errors.Is(err, ErrConfirm) {
		t.Fatalf("upload: %v", err)
	}
	if rl, _ := r.store.ListRestores(ctx, d.ID); len(rl) != 0 || len(execLines(r)) != 0 || r.svc.Jobs(d.ID) != "" {
		t.Fatalf("something started: %+v %v", rl, execLines(r))
	}
}

func TestRestoreOfFailedBackupRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	b, _ := r.store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
	_ = r.store.FinishBackup(ctx, b.ID, Backup{Status: JobFailed, Error: "x"})
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "Shop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}
