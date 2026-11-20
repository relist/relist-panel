package databases

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"relist-panel/internal/agefile"
)

// ErrBadUpload is an uploaded file that is not readable gzip or SQL.
var ErrBadUpload = errors.New("the uploaded file is not valid gzip")

// ErrNeedPassphrase is an age upload restored without its passphrase.
var ErrNeedPassphrase = errors.New("this file is encrypted: enter its passphrase")

var ErrChangedFile = errors.New("the backup file changed since it was written; it was not restored")

// fileSHA is the sha256 of the file at path, hex.
func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RestoreFromBackup restores a succeeded Backup of Database id into target.
// Everything before the background job runs here and answers with an error:
// the confirmation, the target, and the file's sha256. The running row is
// returned. With asBackedUp, a new target is created the way the Backup's
// state says (ADR-0050).
func (s *Service) RestoreFromBackup(ctx context.Context, id, backupID int64, target, confirm string, asBackedUp bool) (Restore, error) {
	d, dp, err := s.acquire(ctx, id, JobKindRestore)
	if err != nil {
		return Restore{}, err
	}
	release := func() { s.endJob(id) }
	replace, err := s.checkRestore(ctx, d, dp, target, confirm)
	if err != nil {
		release()
		return Restore{}, err
	}
	path, b, err := s.BackupPath(ctx, id, backupID)
	if err != nil {
		release()
		return Restore{}, err
	}
	if b.LogicalName == "" {
		release()
		return Restore{}, ErrNotRestorable
	}
	sum, err := fileSHA(path)
	if err != nil {
		release()
		return Restore{}, err
	}
	if sum != b.SHA256 {
		release()
		return Restore{}, ErrChangedFile
	}
	r, err := s.d.Store.InsertRestore(ctx, Restore{DatabaseID: id, Source: "backup", BackupID: &backupID, Target: target})
	if err != nil {
		release()
		return Restore{}, err
	}
	var from *BackupState
	if asBackedUp {
		from = &b.State
	}
	s.startRestore(ctx, d, dp, r, path, replace, false, from)
	return r, nil
}

// RestoreFromUpload restores an uploaded .sql, .sql.gz or .age of one
// database into target. The body is streamed to disk; an age file (told by
// its binary header) is decrypted with passphrase first, then gzip is
// detected by content and the text is checked (CheckUpload) before anything
// runs.
func (s *Service) RestoreFromUpload(ctx context.Context, id int64, target, confirm, name, passphrase string, body io.Reader) (Restore, error) {
	d, dp, err := s.acquire(ctx, id, JobKindRestore)
	if err != nil {
		return Restore{}, err
	}
	release := func() { s.endJob(id) }
	replace, err := s.checkRestore(ctx, d, dp, target, confirm)
	if err != nil {
		release()
		return Restore{}, err
	}
	dir := filepath.Join(s.dbDir(id), "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		release()
		return Restore{}, err
	}
	f, err := os.CreateTemp(dir, "*.upload") // mode 0600
	if err != nil {
		release()
		return Restore{}, err
	}
	path := f.Name()
	fail := func(err error) (Restore, error) {
		_ = os.Remove(path)
		release()
		return Restore{}, err
	}
	if _, err := io.Copy(f, body); err != nil {
		_ = f.Close()
		return fail(fmt.Errorf("read the uploaded file: %w", err))
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	if path, err = decryptUpload(dir, path, passphrase); err != nil {
		return fail(err)
	}
	if err := checkUploadFile(d.Engine, path); err != nil {
		return fail(err)
	}
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if len(name) > 255 {
		name = name[:255]
	}
	r, err := s.d.Store.InsertRestore(ctx, Restore{DatabaseID: id, Source: "upload", UploadName: name, Target: target})
	if err != nil {
		return fail(err)
	}
	s.startRestore(ctx, d, dp, r, path, replace, true, nil)
	return r, nil
}

// decryptUpload turns an age upload at path into its plain file in dir and
// returns the new path, removing the encrypted one. A file without the age
// header passes through untouched. On an error the returned path is the one
// still on disk, for the caller to remove; nothing else is left behind.
func decryptUpload(dir, path, passphrase string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return path, err
	}
	defer f.Close()
	head := make([]byte, 22)
	n, _ := io.ReadFull(f, head)
	if !agefile.IsAge(head[:n]) {
		return path, nil
	}
	if passphrase == "" {
		return path, ErrNeedPassphrase
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return path, err
	}
	r, err := agefile.Decrypt(passphrase, f)
	if err != nil {
		return path, err
	}
	out, err := os.CreateTemp(dir, "*.upload") // mode 0600
	if err != nil {
		return path, err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return path, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return path, err
	}
	_ = os.Remove(path)
	return out.Name(), nil
}

// checkRestore is the confirmation and the target: whether target replaces an
// existing logical database or creates one.
func (s *Service) checkRestore(ctx context.Context, d Database, dp Dumper, target, confirm string) (replace bool, err error) {
	if confirm != d.Name {
		return false, ErrConfirm
	}
	have, err := s.listLogical(ctx, d, dp)
	if err != nil {
		return false, err
	}
	return checkTarget(target, have)
}

// openSQL opens the file at path as plain SQL. gzip is told by its magic
// bytes, never by the file's name.
func openSQL(path string) (io.Reader, io.Closer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	br := bufio.NewReader(f)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			_ = f.Close()
			return nil, nil, fmt.Errorf("%w: %v", ErrBadUpload, err)
		}
		return zr, f, nil
	}
	return br, f, nil
}

// checkUploadFile runs CheckUpload over the file's plain text.
func checkUploadFile(engine, path string) error {
	in, c, err := openSQL(path)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := CheckUpload(engine, in); err != nil {
		var f ErrForbiddenStatement
		if errors.As(err, &f) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrBadUpload, err)
	}
	return nil
}

// startRestore runs the restore of the file at path in the background and
// releases the job slot when it ends. An upload's file is removed afterwards.
// from, when set, is the Backup state a new target is created from.
func (s *Service) startRestore(ctx context.Context, d Database, dp Dumper, r Restore, path string, replace, temp bool, from *BackupState) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(d.ID)
		if temp {
			defer os.Remove(path)
		}
		ctx := context.WithoutCancel(ctx)
		s.containRows(ctx, d.ID, "restore", func() { s.executeRestore(ctx, d, dp, r, path, replace, from) })
	}()
}

func (s *Service) finishRestore(ctx context.Context, r Restore, pre *int64, status, errMsg, log string) {
	_ = s.d.Store.FinishRestore(ctx, r.ID, Restore{Status: status, Error: errMsg, Log: log, PreBackupID: pre})
}

// admin runs SQL through the engine's admin command and returns what it
// printed. A non-zero exit is an error holding the stderr tail.
func (s *Service) admin(ctx context.Context, d Database, dp Dumper, sql string) (out, log string, err error) {
	var stdout bytes.Buffer
	errOut := &tailBuffer{max: logLimit}
	env := dp.Env(d.Password)
	if d.Engine == "postgres" {
		// The same catalog search_path as the panel's other superuser
		// sessions: a startup option outranks any role or database setting.
		env = append(env, "PGOPTIONS=-c search_path=pg_catalog,pg_temp")
	}
	code, err := s.d.Host.Exec(ctx, ContainerName(d.ID), ExecRequest{
		Cmd: dp.AdminCmd(), Env: env, Stdin: strings.NewReader(sql), Stdout: &stdout, Stderr: errOut})
	if err == nil && code != 0 {
		err = fmt.Errorf("the database command exited with code %d", code)
	}
	return stdout.String(), errOut.String(), err
}

func (s *Service) executeRestore(ctx context.Context, d Database, dp Dumper, r Restore, path string, replace bool, from *BackupState) {
	target := r.Target
	var pre *int64
	if replace {
		b := s.backupNow(ctx, d, dp, target, TriggerPreRestore, func(b Backup) {
			pre = &b.ID
			_ = s.d.Store.SetRestorePreBackup(ctx, r.ID, b.ID)
		})
		if b.Status != JobSucceeded {
			s.finishRestore(ctx, r, pre, JobFailed, "the pre-restore backup failed: "+b.Error, b.Log)
			return
		}
	}

	// A replace keeps the target's owner, ACL and per-database settings,
	// which DROP DATABASE deletes and a one-database dump does not carry.
	st, log, err := s.captureState(ctx, d, dp, target)
	if err != nil {
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), log)
		return
	}
	if !replace && st.Prev != "" {
		// It was created after the target was checked; nothing was backed up.
		s.finishRestore(ctx, r, pre, JobFailed, "the database "+target+" now exists; it was not replaced", "")
		return
	}
	note := ""
	if !replace && from != nil {
		st.From, note = s.createdAs(ctx, d, dp, target, *from)
	}
	log, err = s.restoreInto(ctx, d, dp, st, path)
	if note != "" {
		log = note + "\n" + log
	}
	if err != nil {
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), log)
		return
	}
	s.finishRestore(ctx, r, pre, JobSucceeded, "", log)
}

// createdAs is how a new target is created from a Backup's state: NewSQL's
// from, and a note for the log when the state cannot be used. A Postgres
// owner is used only while it exists and is no superuser, which would make
// the owner's code run as one (ADR-0048); otherwise postgres owns it.
func (s *Service) createdAs(ctx context.Context, d Database, dp Dumper, target string, st BackupState) (from, note string) {
	if st.Prev == "" {
		return "", ""
	}
	if d.Engine != "postgres" {
		return st.Prev, ""
	}
	owner, _, _ := strings.Cut(st.Prev, "\t")
	if owner == "" || owner == "postgres" {
		return "", ""
	}
	out, _, err := s.admin(ctx, d, dp, "SELECT rolsuper FROM pg_roles WHERE rolname = "+pgLit(owner)+";")
	switch strings.TrimSpace(out) {
	case "f":
		return owner, ""
	case "t":
		return "", owner + " is a superuser now, so postgres owns " + target + "."
	}
	if err != nil {
		return "", "The owner " + owner + " could not be checked, so postgres owns " + target + "."
	}
	return "", owner + " no longer exists, so postgres owns " + target + "."
}
