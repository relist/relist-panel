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
// returned.
func (s *Service) RestoreFromBackup(ctx context.Context, id, backupID int64, target, confirm string) (Restore, error) {
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
	s.startRestore(ctx, d, dp, r, path, replace, false)
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
	s.startRestore(ctx, d, dp, r, path, replace, true)
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
func (s *Service) startRestore(ctx context.Context, d Database, dp Dumper, r Restore, path string, replace, temp bool) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(d.ID)
		if temp {
			defer os.Remove(path)
		}
		s.executeRestore(context.WithoutCancel(ctx), d, dp, r, path, replace)
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
	code, err := s.d.Host.Exec(ctx, ContainerName(d.ID), ExecRequest{
		Cmd: dp.AdminCmd(), Env: dp.Env(d.Password), Stdin: strings.NewReader(sql), Stdout: &stdout, Stderr: errOut})
	if err == nil && code != 0 {
		err = fmt.Errorf("the database command exited with code %d", code)
	}
	return stdout.String(), errOut.String(), err
}

func (s *Service) executeRestore(ctx context.Context, d Database, dp Dumper, r Restore, path string, replace bool) {
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

	prev, log, err := s.admin(ctx, d, dp, dp.InspectSQL(target))
	if err != nil {
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), log)
		return
	}
	prev = strings.TrimSpace(prev)
	if !replace && prev != "" {
		// It was created after the target was checked; nothing was backed up.
		s.finishRestore(ctx, r, pre, JobFailed, "the database "+target+" now exists; it was not replaced", "")
		return
	}
	if _, log, err := s.admin(ctx, d, dp, dp.ReplaceSQL(target, prev)); err != nil {
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), log)
		return
	}

	in, c, err := openSQL(path)
	if err != nil {
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), "")
		return
	}
	defer c.Close()
	cmd, extra := dp.RestoreCmd(target)
	errOut := &tailBuffer{max: logLimit}
	code, err := s.d.Host.Exec(ctx, ContainerName(d.ID), ExecRequest{Cmd: cmd, Env: execEnv(dp, d.Password, extra), Stdin: in, Stderr: errOut})
	switch {
	case err != nil:
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), errOut.String())
	case code != 0:
		s.finishRestore(ctx, r, pre, JobFailed, fmt.Sprintf("the restore command exited with code %d", code), errOut.String())
	default:
		s.finishRestore(ctx, r, pre, JobSucceeded, "", errOut.String())
	}
}
