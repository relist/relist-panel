package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"relist-panel/internal/agefile"
	"relist-panel/internal/backupdest"
)

// JobKindUpload is the job slot a Retry upload takes.
const JobKindUpload = "upload"

var (
	// ErrNoDestinations is a schedule's destination on a panel without any.
	ErrNoDestinations = errors.New("Backup destinations are not available")
	// ErrNoFailedUpload is a retry of a Backup whose copy did not fail.
	ErrNoFailedUpload = errors.New("only a failed copy can be retried")
)

func (s *Service) filePath(id int64, b Backup) string {
	return filepath.Join(s.dbDir(id), "backups", filepath.Base(b.FileName))
}

// upload copies Backup b's file to destination destID and records the result
// in an upload row. The Backup stays succeeded whatever happens. A failure is
// the row's error, and the returned Upload says so; its text never carries the
// destination's secret.
func (s *Service) upload(ctx context.Context, d Database, b Backup, destID int64) Upload {
	if s.d.Destinations == nil {
		return Upload{BackupID: b.ID, DestinationID: destID, Status: UploadFailed, Error: ErrNoDestinations.Error()}
	}
	dest, remote, err := s.d.Destinations.Open(ctx, destID)
	if err != nil {
		return Upload{BackupID: b.ID, DestinationID: destID, Status: UploadFailed, Error: err.Error()}
	}
	key := backupdest.ObjectKey(dest.KeyPrefix(), d.Slug, d.ID, b.LogicalName, b.FileName)
	if dest.Encrypt {
		key += ".age"
	}
	u, err := s.d.Store.InsertUpload(ctx, Upload{BackupID: b.ID, DestinationID: destID, ObjectKey: key, Status: UploadRunning, Encrypted: dest.Encrypt})
	if err != nil {
		return Upload{BackupID: b.ID, DestinationID: destID, DestinationName: dest.Name, Status: UploadFailed, Error: err.Error()}
	}
	path := s.filePath(d.ID, b)
	if dest.Encrypt {
		// Encrypt beside the Backup first, so the destination gets the
		// encrypted file with its exact size.
		if tmp, eerr := encryptToTemp(path, dest.Passphrase); eerr != nil {
			err = eerr
		} else {
			err = s.put(ctx, remote, key, tmp)
			os.Remove(tmp)
		}
	} else {
		err = s.put(ctx, remote, key, path)
	}
	status, msg := UploadUploaded, ""
	if err != nil {
		status, msg = UploadFailed, scrub(err.Error(), dest.Passphrase)
	}
	u.Status, u.Error = status, msg
	if ferr := s.d.Store.FinishUpload(ctx, u.ID, status, msg, s.d.Now()); ferr != nil {
		log.Printf("databases: record upload %d: %v", u.ID, ferr)
	}
	return u
}

// encryptToTemp writes path's age encryption under passphrase to a temp file
// in the same directory and returns its path; on any error nothing is left.
func encryptToTemp(path, passphrase string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dir, base := filepath.Split(path)
	tmp, err := os.CreateTemp(dir, base+"-*.age.tmp")
	if err != nil {
		return "", err
	}
	if err := agefile.Encrypt(tmp, passphrase, src); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// errStalled ends an upload that sent nothing for uploadStall.
var errStalled = errors.New("stalled")

// progressReader records when the upload last read from the file.
type progressReader struct {
	r    io.Reader
	last atomic.Int64 // unix nanoseconds
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.last.Store(time.Now().UnixNano())
	}
	return n, err
}

// put sends the file with its exact size, so nothing is buffered in memory.
// An upload that reads nothing for uploadStall, including the wait for the
// bucket's answer after the last byte, is cut off: a bucket that stops
// answering must not hold the job slot, and the scheduler, for ever.
func (s *Service) put(ctx context.Context, remote backupdest.Remote, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	pr := &progressReader{r: f}
	pr.last.Store(time.Now().UnixNano())
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(s.uploadStall / 4)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if time.Since(time.Unix(0, pr.last.Load())) > s.uploadStall {
					cancel(errStalled)
					return
				}
			}
		}
	}()
	err = remote.Put(ctx, key, pr, fi.Size())
	if errors.Is(context.Cause(ctx), errStalled) {
		return fmt.Errorf("the upload made no progress for %s", s.uploadStall)
	}
	return err
}

// destinationName is the name to show for a destination, falling back to its ID.
func (s *Service) destinationName(ctx context.Context, id int64) string {
	if s.d.Destinations != nil {
		if dest, _, err := s.d.Destinations.Open(ctx, id); err == nil {
			return dest.Name
		}
	}
	return fmt.Sprintf("destination %d", id)
}

// uploadFailure is the schedule's last_error for a failed copy.
func (s *Service) uploadFailure(ctx context.Context, u Upload) string {
	name := u.DestinationName
	if name == "" {
		name = s.destinationName(ctx, u.DestinationID)
	}
	return "upload to " + name + " failed: " + u.Error
}

// RetryUpload copies a Backup whose copy failed again, in the background.
// Only a failed copy of a Backup whose file is still here can be retried.
func (s *Service) RetryUpload(ctx context.Context, id, backupID int64) error {
	if err := s.gate(ctx); err != nil {
		return err
	}
	if err := s.scheduleDatabase(ctx, id); err != nil {
		return err
	}
	b, err := s.d.Store.GetBackup(ctx, id, backupID)
	if err != nil {
		return err
	}
	if b.LocalRemovedAt != nil {
		return ErrRemoteOnly
	}
	list, err := s.d.Store.ListBackups(ctx, id)
	if err != nil {
		return err
	}
	var failed *Upload
	for _, x := range list {
		if x.ID == backupID {
			failed = x.Upload
		}
	}
	if failed == nil || failed.Status != UploadFailed || b.Status != JobSucceeded {
		return ErrNoFailedUpload
	}
	if !s.tryJob(id, JobKindUpload) {
		return ErrBusy
	}
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		s.endJob(id)
		return err
	}
	if err := s.d.Store.DeleteUpload(ctx, failed.ID); err != nil {
		s.endJob(id)
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		ctx := context.WithoutCancel(ctx)
		s.contain(ctx, id, "retry upload", func() {
			u := s.upload(ctx, d, b, failed.DestinationID)
			if u.Status == UploadFailed {
				log.Printf("databases: retry upload of backup %d: %s", backupID, u.Error)
			}
		})
	}()
	return nil
}

// ErrRemoteDelete is a copy that could not be deleted from its Backup
// destination; it is wrapped with the destination's name and the remote error.
var ErrRemoteDelete = errors.New("could not delete the copy in the Backup destination")

// deleteCopy deletes one live copy from its destination and records it.
func (s *Service) deleteCopy(ctx context.Context, remotes map[int64]backupdest.Remote, u Upload) error {
	remote, ok := remotes[u.DestinationID]
	if !ok {
		if s.d.Destinations == nil {
			return fmt.Errorf("%w %q: %s", ErrRemoteDelete, u.DestinationName, ErrNoDestinations)
		}
		_, r, err := s.d.Destinations.Open(ctx, u.DestinationID)
		if err != nil {
			return fmt.Errorf("%w %q: %s", ErrRemoteDelete, u.DestinationName, err)
		}
		remotes[u.DestinationID], remote = r, r
	}
	dctx, cancel := context.WithTimeout(ctx, s.remoteTimeout)
	err := remote.Delete(dctx, u.ObjectKey)
	cancel()
	if err != nil {
		return fmt.Errorf("%w %q: %s", ErrRemoteDelete, u.DestinationName, err)
	}
	return s.d.Store.MarkUploadDeleted(ctx, u.ID, s.d.Now())
}

// rotateRemote deletes the copies in the schedule's current destination of
// scheduled, unpinned Backups that break sc.Remote. Copies in a former
// destination are never touched. A Backup whose file is gone goes with its
// last copy. A failed delete is logged and tried again next tick.
func (s *Service) rotateRemote(ctx context.Context, id int64, sc Schedule) error {
	if sc.DestinationID == nil {
		return nil
	}
	live, err := s.d.Store.LiveUploads(ctx, id, *sc.DestinationID)
	if err != nil || len(live) == 0 {
		return err
	}
	all, err := s.d.Store.ListBackups(ctx, id)
	if err != nil {
		return err
	}
	byID := map[int64]Backup{}
	for _, b := range all {
		byID[b.ID] = b
	}
	copies := map[int64]Upload{}
	var items []RotItem
	for _, u := range live {
		b, ok := byID[u.BackupID]
		if !ok || b.Trigger != TriggerScheduled || b.Pinned {
			continue
		}
		copies[b.ID] = u
		items = append(items, RotItem{ID: b.ID, Group: b.LogicalName, At: b.StartedAt, OK: true})
	}
	remotes := map[int64]backupdest.Remote{}
	var errs []error
	for _, bid := range Rotate(items, sc.Remote, s.d.Now()) {
		if err := s.deleteCopy(ctx, remotes, copies[bid]); err != nil {
			errs = append(errs, err)
			continue
		}
		b := byID[bid]
		if b.LocalRemovedAt != nil || !s.fileExists(id, b) {
			if err := s.d.Store.DeleteBackup(ctx, bid); err != nil && !errors.Is(err, ErrNotFound) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) fileExists(id int64, b Backup) bool {
	if b.FileName == "" {
		return false
	}
	_, err := os.Stat(s.filePath(id, b))
	return err == nil
}

// deleteLiveCopies deletes every live copy of the given Backups from its
// destination. The first failure stops it and is returned (ErrRemoteDelete);
// the copies already deleted stay recorded as deleted.
func (s *Service) deleteLiveCopies(ctx context.Context, databaseID int64, only map[int64]bool) error {
	live, err := s.d.Store.LiveUploads(ctx, databaseID, 0)
	if err != nil {
		return err
	}
	remotes := map[int64]backupdest.Remote{}
	for _, u := range live {
		if only != nil && !only[u.BackupID] {
			continue
		}
		if err := s.deleteCopy(ctx, remotes, u); err != nil {
			return err
		}
	}
	return nil
}
