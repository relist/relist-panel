package databases

import (
	"bufio"
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
)

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

// RestoreFromBackup restores a succeeded Backup of Database id after a
// pre_restore Backup. The file's sha256 is checked before anything else runs.
// The restore runs in the background; the running row is returned.
func (s *Service) RestoreFromBackup(ctx context.Context, id, backupID int64, confirm string) (Restore, error) {
	d, dp, err := s.acquire(ctx, id, JobKindRestore)
	if err != nil {
		return Restore{}, err
	}
	release := func() { s.endJob(id) }
	if confirm != d.Name {
		release()
		return Restore{}, ErrConfirm
	}
	path, b, err := s.BackupPath(ctx, id, backupID)
	if err != nil {
		release()
		return Restore{}, err
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
	r, err := s.d.Store.InsertRestore(ctx, Restore{DatabaseID: id, Source: "backup", BackupID: &backupID})
	if err != nil {
		release()
		return Restore{}, err
	}
	s.startRestore(ctx, d, dp, r, path, b.Scope)
	return r, nil
}

// RestoreFromUpload restores an uploaded .sql or .sql.gz after a pre_restore
// Backup. The body is streamed to disk first; gzip is detected by content.
func (s *Service) RestoreFromUpload(ctx context.Context, id int64, confirm, name string, body io.Reader) (Restore, error) {
	d, dp, err := s.acquire(ctx, id, JobKindRestore)
	if err != nil {
		return Restore{}, err
	}
	release := func() { s.endJob(id) }
	if confirm != d.Name {
		release()
		return Restore{}, ErrConfirm
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
	if _, err := io.Copy(f, body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		release()
		return Restore{}, fmt.Errorf("read the uploaded file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		release()
		return Restore{}, err
	}
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if len(name) > 255 {
		name = name[:255]
	}
	r, err := s.d.Store.InsertRestore(ctx, Restore{DatabaseID: id, Source: "upload", UploadName: name})
	if err != nil {
		_ = os.Remove(path)
		release()
		return Restore{}, err
	}
	s.startRestore(ctx, d, dp, r, path, Scope{All: true}, true)
	return r, nil
}

// startRestore runs the restore of the file at path in the background and
// releases the job slot when it ends. temp files are removed afterwards.
func (s *Service) startRestore(ctx context.Context, d Database, dp Dumper, r Restore, path string, sc Scope, temp ...bool) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(d.ID)
		if len(temp) > 0 && temp[0] {
			defer os.Remove(path)
		}
		s.executeRestore(context.WithoutCancel(ctx), d, dp, r, path, sc)
	}()
}

// terminateSQL ends the sessions on the databases a restore replaces, so
// DROP DATABASE can proceed. Names are quoted as quote_literal does.
func terminateSQL(sc Scope) string {
	filter := "datname IS NOT NULL AND datname <> 'template1'"
	if !sc.All && len(sc.Databases) > 0 {
		quoted := make([]string, len(sc.Databases))
		for i, n := range sc.Databases {
			quoted[i] = "'" + strings.ReplaceAll(n, "'", "''") + "'"
		}
		filter = "datname IN (" + strings.Join(quoted, ", ") + ")"
	}
	return "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND " + filter
}

func (s *Service) finishRestore(ctx context.Context, r Restore, pre *int64, status, errMsg, log string) {
	_ = s.d.Store.FinishRestore(ctx, r.ID, Restore{Status: status, Error: errMsg, Log: log, PreBackupID: pre})
}

func (s *Service) executeRestore(ctx context.Context, d Database, dp Dumper, r Restore, path string, sc Scope) {
	var pre *int64
	b := s.backupNow(ctx, d, dp, Scope{All: true}, TriggerPreRestore, func(b Backup) {
		pre = &b.ID
		_ = s.d.Store.SetRestorePreBackup(ctx, r.ID, b.ID)
	})
	if b.Status != JobSucceeded {
		s.finishRestore(ctx, r, pre, JobFailed, "the pre-restore backup failed: "+b.Error, b.Log)
		return
	}

	name := ContainerName(d.ID)
	if d.Engine == "postgres" {
		log := &tailBuffer{max: logLimit}
		code, err := s.d.Host.Exec(ctx, name, ExecRequest{
			Cmd: []string{"psql", "-U", "postgres", "-d", "template1", "-c", terminateSQL(sc)}, Env: dp.Env(d.Password), Stderr: log,
		})
		if err == nil && code != 0 {
			err = fmt.Errorf("closing the other sessions exited with code %d", code)
		}
		if err != nil {
			s.finishRestore(ctx, r, pre, JobFailed, err.Error(), log.String())
			return
		}
	}

	f, err := os.Open(path)
	if err != nil {
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), "")
		return
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var in io.Reader = br
	// gzip is told by its magic bytes, never by the file's name.
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			s.finishRestore(ctx, r, pre, JobFailed, "the file is not valid gzip: "+err.Error(), "")
			return
		}
		in = zr
	}
	log := &tailBuffer{max: logLimit}
	code, err := s.d.Host.Exec(ctx, name, ExecRequest{Cmd: dp.RestoreCmd(), Env: dp.Env(d.Password), Stdin: in, Stderr: log})
	switch {
	case err != nil:
		s.finishRestore(ctx, r, pre, JobFailed, err.Error(), log.String())
	case code != 0:
		s.finishRestore(ctx, r, pre, JobFailed, fmt.Sprintf("the restore command exited with code %d", code), log.String())
	case dp.RestoreFailed(log.String()):
		s.finishRestore(ctx, r, pre, JobFailed, "the restore reported errors", log.String())
	default:
		s.finishRestore(ctx, r, pre, JobSucceeded, "", log.String())
	}
}
