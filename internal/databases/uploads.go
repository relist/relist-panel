package databases

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	key := backupdest.ObjectKey(dest.S3.Prefix, d.Slug, d.ID, b.LogicalName, b.FileName)
	u, err := s.d.Store.InsertUpload(ctx, Upload{BackupID: b.ID, DestinationID: destID, ObjectKey: key, Status: UploadRunning})
	if err != nil {
		return Upload{BackupID: b.ID, DestinationID: destID, DestinationName: dest.Name, Status: UploadFailed, Error: err.Error()}
	}
	err = s.put(ctx, remote, key, s.filePath(d.ID, b))
	status, msg := UploadUploaded, ""
	if err != nil {
		status, msg = UploadFailed, err.Error()
	}
	u.Status, u.Error = status, msg
	if ferr := s.d.Store.FinishUpload(ctx, u.ID, status, msg, s.d.Now()); ferr != nil {
		log.Printf("databases: record upload %d: %v", u.ID, ferr)
	}
	return u
}

// put sends the file with its exact size, so nothing is buffered in memory.
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
	return remote.Put(ctx, key, f, fi.Size())
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
		u := s.upload(ctx, d, b, failed.DestinationID)
		if u.Status == UploadFailed {
			log.Printf("databases: retry upload of backup %d: %s", backupID, u.Error)
		}
	}()
	return nil
}
