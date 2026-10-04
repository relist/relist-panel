package metrics

import (
	"context"
	"log"
	"time"

	"relist-panel/internal/hostmetrics"
)

// Recorder is the minute-close writer: it turns the sampler's minute-close
// hook into rollup rows — one host row from the rollup the sampler hands it,
// the Service rows from the collector's pending sweeps, then the retention
// delete. It runs on the sampler's loop goroutine, so a storage failure is
// logged and lived with, never panicked on: a missed row is a gap the screens
// already know how to render.
type Recorder struct {
	store     *Store
	collector *hostmetrics.ServiceCollector
	now       func() time.Time
}

// NewRecorder constructs the writer over the rollup store and the collector
// whose sweeps feed the Service rows.
func NewRecorder(store *Store, collector *hostmetrics.ServiceCollector, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	return &Recorder{store: store, collector: collector, now: now}
}

// OnMinuteClose persists the minute that just closed. The host rollup arrives
// averaged over the samples the minute actually saw (hostmetrics.MinuteAverage);
// the Service side is drained from the collector, which averages its own
// pending sweeps the same way. A minute a restart cut short still writes its
// row, however few samples it saw.
func (r *Recorder) OnMinuteClose(rollup hostmetrics.HostSample) {
	ctx := context.Background()

	if err := r.store.WriteHostRollup(ctx, rollup); err != nil {
		log.Printf("write host metric rollup: %v", err)
	}
	if err := r.store.WriteServiceRollups(ctx, r.collector.CloseMinute()); err != nil {
		log.Printf("write service metric rollups: %v", err)
	}
	if err := r.store.DeleteRollupsBefore(ctx, r.now().Add(-RetentionHorizon)); err != nil {
		log.Printf("delete expired metric rollups: %v", err)
	}
}
