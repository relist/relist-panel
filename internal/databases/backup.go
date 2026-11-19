package databases

import (
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
	"slices"
	"strings"
)

// logLimit is how much of a dump's or restore's stderr a row keeps: the last
// 64 KiB.
const logLimit = 64 * 1024

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

// String is the kept tail, with a cut-in-half character dropped.
func (t *tailBuffer) String() string { return strings.ToValidUTF8(string(t.buf), "") }

// begin checks the preconditions of a job on Database id and takes its slot:
// no Migration or Panel update lock, a live Database, a free slot, and a
// running container. pre, when set, runs after the Database is read and
// before the slot is checked. It holds the Database's lock only for this
// check, so Rename keeps working during the job.
func (s *Service) begin(ctx context.Context, id int64, kind string, pre func(Database) error) (Database, error) {
	if err := s.gate(ctx); err != nil {
		return Database{}, err
	}
	unlock := s.lock(id)
	defer unlock()
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return Database{}, err
	}
	if d.DeletedAt != nil {
		return Database{}, ErrNotFound
	}
	if pre != nil {
		if err := pre(d); err != nil {
			return Database{}, err
		}
	}
	if s.Jobs(id) != "" {
		return Database{}, ErrBusy
	}
	st, err := s.d.Host.Inspect(ctx, ContainerName(id))
	if err != nil {
		return Database{}, err
	}
	if !st.Exists || !st.Running {
		return Database{}, ErrNotRunning
	}
	if err := s.beginJob(id, kind); err != nil {
		return Database{}, err
	}
	return d, nil
}

// acquire is begin for a job that dumps: it also needs the data dir and an
// engine with Backups.
func (s *Service) acquire(ctx context.Context, id int64, kind string) (Database, Dumper, error) {
	if err := s.gate(ctx); err != nil {
		return Database{}, nil, err
	}
	if s.d.DataDir == "" {
		return Database{}, nil, errors.New("backups are not configured")
	}
	var dp Dumper
	d, err := s.begin(ctx, id, kind, func(d Database) (err error) {
		dp, err = DumperFor(d.Engine)
		return err
	})
	if err != nil {
		return Database{}, nil, err
	}
	return d, dp, nil
}

// listLogical runs the engine's list command and returns the logical databases.
func (s *Service) listLogical(ctx context.Context, d Database, dp Dumper) ([]string, error) {
	errOut := &tailBuffer{max: logLimit}
	var stdout bytes.Buffer
	code, err := s.d.Host.Exec(ctx, ContainerName(d.ID), ExecRequest{Cmd: dp.ListCmd(), Env: dp.Env(d.Password), Stdout: &stdout, Stderr: errOut})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("listing databases exited with code %d: %s", code, strings.TrimSpace(errOut.String()))
	}
	system := dp.SystemDatabases()
	names := []string{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !slices.Contains(system, line) {
			names = append(names, line)
		}
	}
	slices.Sort(names)
	return names, nil
}

// ListLogicalDatabases lists the databases inside Database id, without the
// engine's system ones.
func (s *Service) ListLogicalDatabases(ctx context.Context, id int64) ([]string, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.DeletedAt != nil {
		return nil, ErrNotFound
	}
	dp, err := DumperFor(d.Engine)
	if err != nil {
		return nil, err
	}
	st, err := s.d.Host.Inspect(ctx, ContainerName(id))
	if err != nil {
		return nil, err
	}
	if !st.Exists || !st.Running {
		return nil, ErrNotRunning
	}
	return s.listLogical(ctx, d, dp)
}

// BackupRequest is what a Backup run covers: every logical database, or the
// named ones.
type BackupRequest struct {
	All       bool     `json:"all"`
	Databases []string `json:"databases"`
}

// StartBackup checks the request, inserts one running row per logical
// database (names sorted), and dumps them one after another in one job. The
// rows are returned at once.
func (s *Service) StartBackup(ctx context.Context, id int64, req BackupRequest) ([]Backup, error) {
	d, dp, err := s.acquire(ctx, id, JobKindBackup)
	if err != nil {
		return nil, err
	}
	rows, err := s.insertRun(ctx, d, dp, req)
	if err != nil {
		s.endJob(id)
		return nil, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		ctx := context.WithoutCancel(ctx)
		s.contain(ctx, id, "backup", func() {
			for _, b := range rows {
				s.executeBackup(ctx, d, dp, b)
			}
		})
	}()
	return rows, nil
}

// insertRun resolves req to logical database names and inserts a running
// manual Backup row for each. Nothing is inserted when the request is wrong.
func (s *Service) insertRun(ctx context.Context, d Database, dp Dumper, req BackupRequest) ([]Backup, error) {
	if !req.All && len(req.Databases) == 0 {
		return nil, ErrNoScope
	}
	if !req.All {
		for _, n := range req.Databases {
			if err := checkName(n); err != nil {
				return nil, err
			}
		}
	}
	have, err := s.listLogical(ctx, d, dp)
	if err != nil {
		return nil, err
	}
	names := have
	if !req.All {
		names = slices.Clone(req.Databases)
		slices.Sort(names)
		names = slices.Compact(names)
		var missing []string
		for _, n := range names {
			if !slices.Contains(have, n) {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			return nil, ErrUnknownLogical{Names: missing}
		}
	}
	if len(names) == 0 {
		return nil, ErrNothingToBackUp
	}
	var rows []Backup
	for _, n := range names {
		b, err := s.d.Store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, LogicalName: n})
		if err != nil {
			for _, done := range rows { // none of them will run
				_ = s.d.Store.FinishBackup(ctx, done.ID, Backup{Status: JobFailed, Error: err.Error()})
			}
			return nil, err
		}
		rows = append(rows, b)
	}
	return rows, nil
}

// backupAll backs up every logical database for pre_update, one after
// another. ok is false if any failed, and firstErr names the first one.
func (s *Service) backupAll(ctx context.Context, d Database, dp Dumper, trigger string) (ok bool, firstErr string) {
	names, err := s.listLogical(ctx, d, dp)
	if err != nil {
		return false, err.Error()
	}
	ok = true
	for _, n := range names {
		if b := s.backupNow(ctx, d, dp, n, trigger, nil); b.Status != JobSucceeded {
			if ok {
				firstErr = n + ": " + b.Error
			}
			ok = false
		}
	}
	return ok, firstErr
}

// backupNow records and runs a Backup of one logical database before
// returning: the pre_restore Backup a restore takes inside itself.
func (s *Service) backupNow(ctx context.Context, d Database, dp Dumper, logical, trigger string, onInsert func(Backup)) Backup {
	if err := checkName(logical); err != nil {
		return Backup{Status: JobFailed, Error: err.Error()}
	}
	b, err := s.d.Store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: trigger, LogicalName: logical})
	if err != nil {
		return Backup{Status: JobFailed, Error: err.Error()}
	}
	if onInsert != nil {
		onInsert(b)
	}
	return s.executeBackup(ctx, d, dp, b)
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// executeBackup dumps into <name>.partial and renames it on success. Any
// failure removes the partial and records the Backup as failed.
func (s *Service) executeBackup(ctx context.Context, d Database, dp Dumper, b Backup) Backup {
	dir := filepath.Join(s.dbDir(d.ID), "backups")
	name := fmt.Sprintf("%s-%d.sql.gz", b.StartedAt.UTC().Format("20060102T150405Z"), b.ID)
	logs := &tailBuffer{max: logLimit}
	size, sum, err := s.dumpTo(ctx, d, dp, b.LogicalName, dir, name, logs)
	fin := Backup{Log: logs.String()}
	if err != nil {
		fin.Status, fin.Error = JobFailed, err.Error()
	} else {
		fin.Status, fin.FileName, fin.Size, fin.SHA256 = JobSucceeded, name, size, sum
	}
	if ferr := s.d.Store.FinishBackup(ctx, b.ID, fin); ferr != nil && err == nil {
		// The file is complete but unrecorded; remove it rather than keep an orphan.
		_ = os.Remove(filepath.Join(dir, name))
		fin.Status, fin.Error, fin.FileName = JobFailed, ferr.Error(), ""
	}
	b.Status, b.Error, b.Log, b.FileName, b.Size, b.SHA256 = fin.Status, fin.Error, fin.Log, fin.FileName, fin.Size, fin.SHA256
	return b
}

// dumpTo writes the dump of one logical database as one gzip stream to dir/name, through a
// .partial file that is renamed only when every step succeeded.
func (s *Service) dumpTo(ctx context.Context, d Database, dp Dumper, logical, dir, name string, stderr io.Writer) (size int64, sum string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, "", err
	}
	partial := filepath.Join(dir, name+".partial")
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	closed := false
	defer func() {
		if err != nil {
			if !closed {
				_ = f.Close()
			}
			_ = os.Remove(partial)
		}
	}()
	h := sha256.New()
	file := &countingWriter{w: io.MultiWriter(f, h)}
	gz := gzip.NewWriter(file)
	if err := checkName(logical); err != nil {
		return 0, "", err
	}
	cmd, extra := dp.DumpCmd(logical)
	code, err := s.d.Host.Exec(ctx, ContainerName(d.ID), ExecRequest{Cmd: cmd, Env: execEnv(dp, d.Password, extra), Stdout: gz, Stderr: stderr})
	if err != nil {
		return 0, "", err
	}
	if code != 0 {
		return 0, "", fmt.Errorf("%s exited with code %d", cmd[0], code)
	}
	if err = gz.Close(); err != nil {
		return 0, "", err
	}
	if err = f.Sync(); err != nil {
		return 0, "", err
	}
	closed = true
	if err = f.Close(); err != nil {
		return 0, "", err
	}
	if err = os.Rename(partial, filepath.Join(dir, name)); err != nil {
		return 0, "", err
	}
	return file.n, hex.EncodeToString(h.Sum(nil)), nil
}

// hasBackups checks that Database id exists and its engine has Backups:
// every backup route answers ErrNoBackups for Redis.
func (s *Service) hasBackups(ctx context.Context, id int64) error {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = DumperFor(d.Engine)
	return err
}

// ListBackups returns Database id's Backups, newest first.
func (s *Service) ListBackups(ctx context.Context, id int64) ([]Backup, error) {
	if err := s.hasBackups(ctx, id); err != nil {
		return nil, err
	}
	return s.d.Store.ListBackups(ctx, id)
}

// ListRestores returns Database id's last 20 restores, newest first.
func (s *Service) ListRestores(ctx context.Context, id int64) ([]Restore, error) {
	if err := s.hasBackups(ctx, id); err != nil {
		return nil, err
	}
	return s.d.Store.ListRestores(ctx, id)
}

// ErrRemoteOnly is a download or restore of a Backup whose file Rotation
// removed and whose only copy is in a Backup destination.
var ErrRemoteOnly = errors.New("this Backup is only in its Backup destination; download it from the bucket")

// BackupPath is the file of a succeeded Backup, for download. A Remote only
// Backup is ErrRemoteOnly; anything else, including a file that is gone, is
// ErrNotFound.
func (s *Service) BackupPath(ctx context.Context, id, backupID int64) (string, Backup, error) {
	if err := s.hasBackups(ctx, id); err != nil {
		return "", Backup{}, err
	}
	b, err := s.d.Store.GetBackup(ctx, id, backupID)
	if err != nil {
		return "", Backup{}, err
	}
	if b.LocalRemovedAt != nil {
		return "", Backup{}, ErrRemoteOnly
	}
	if b.Status != JobSucceeded || b.FileName == "" {
		return "", Backup{}, ErrNotFound
	}
	path := filepath.Join(s.dbDir(id), "backups", filepath.Base(b.FileName))
	if _, err := os.Stat(path); err != nil {
		return "", Backup{}, ErrNotFound
	}
	return path, b, nil
}

// DeleteBackup removes a Backup's file, then its row. A running one is ErrBusy.
func (s *Service) DeleteBackup(ctx context.Context, id, backupID int64) error {
	if err := s.hasBackups(ctx, id); err != nil {
		return err
	}
	b, err := s.d.Store.GetBackup(ctx, id, backupID)
	if err != nil {
		return err
	}
	if b.Status == JobRunning {
		return ErrBusy
	}
	// A running version change restores from its pre-update Backups.
	if b.Trigger == TriggerPreUpdate && s.Jobs(id) == JobKindChangeVersion {
		return ErrBusy
	}
	// The remote copy goes first. If it cannot, nothing else changes, so no
	// object is left behind untracked.
	if err := s.deleteLiveCopies(ctx, id, map[int64]bool{backupID: true}); err != nil {
		return err
	}
	if b.FileName != "" {
		if err := os.Remove(filepath.Join(s.dbDir(id), "backups", filepath.Base(b.FileName))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := s.d.Store.DeleteBackup(ctx, backupID); err != nil {
		return err
	}
	// The last Backup of a tombstone takes the tombstone with it.
	if d, err := s.d.Store.Get(ctx, id); err == nil && d.DeletedAt != nil {
		if left, err := s.d.Store.ListBackups(ctx, id); err == nil && len(left) == 0 {
			return s.removeRecord(ctx, id)
		}
	}
	return nil
}
