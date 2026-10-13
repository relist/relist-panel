package deployments

import (
	"context"
	"time"
)

type completionStore interface {
	Finish(context.Context, int64, Status, string, string, []string, time.Time) error
}

// finishWithRetry writes a known outcome without repeating the job's steps.
// The worker context governs only the persistence wait, not the first attempt:
// a Deploy step shielded from shutdown must still be able to settle its row.
func finishWithRetry(workerCtx context.Context, store completionStore, id int64, o outcome, finishedAt time.Time) error {
	bg := context.WithoutCancel(workerCtx)
	delay := retryDelay
	for {
		err := store.Finish(bg, id, o.status, o.failedStep, o.reason, o.warnings, finishedAt)
		if err == nil || workerCtx.Err() != nil {
			return err
		}
		logf("finish deployment %d: %v; retrying in %s", id, err, delay)
		sleep(workerCtx, delay)
		if workerCtx.Err() != nil {
			return err
		}
		delay = min(2*delay, 30*time.Second)
	}
}
