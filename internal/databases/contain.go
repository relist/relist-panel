package databases

import (
	"context"
	"errors"
	"log"

	"relist-panel/internal/safely"
)

// contain runs one background job of Database id. A panic in body is logged
// with its stack and contained (internal/safely) instead of taking the panel
// down: the Backups, restores and copies the job left running are failed,
// and the Database's last error names the job. It returns that internal
// error, nil when body did not panic; deferred calls inside body have run
// by then.
func (s *Service) contain(ctx context.Context, id int64, job string, body func()) error {
	err := safely.Call(job, func() error {
		body()
		return nil
	})
	if err == nil {
		return nil
	}
	ierr := internalError(err)
	if ferr := s.d.Store.FailRunningOf(ctx, id, ierr.Error()); ferr != nil {
		log.Printf("databases: fail the records of a panicked job: %v", ferr)
	}
	if lerr := s.d.Store.SetLastError(ctx, id, ierr.Error()); lerr != nil {
		log.Printf("databases: record a panicked job: %v", lerr)
	}
	return ierr
}

// internalError is what a panic recovered by internal/safely shows the
// Admin: the stack went to the panel's log.
func internalError(panicked error) error {
	return errors.New("internal error: " + panicked.Error() + " (the panel's log has the details)")
}
