package databases

import (
	"context"
	"log"

	"relist-panel/internal/safely"
)

// contain runs one background job of Database id. A panic in body is logged
// with its stack and contained (internal/safely) instead of taking the panel
// down: the Backups, restores and copies the job left running are failed,
// and the Database's last error names the job. It reports whether body
// panicked; deferred calls inside body have run by then.
func (s *Service) contain(ctx context.Context, id int64, job string, body func()) bool {
	err := safely.Call(job, func() error {
		body()
		return nil
	})
	if err == nil {
		return false
	}
	msg := "internal error: " + err.Error() + " (the panel's log has the details)"
	if ferr := s.d.Store.FailRunningOf(ctx, id, msg); ferr != nil {
		log.Printf("databases: fail the records of a panicked job: %v", ferr)
	}
	if lerr := s.d.Store.SetLastError(ctx, id, msg); lerr != nil {
		log.Printf("databases: record a panicked job: %v", lerr)
	}
	return true
}
