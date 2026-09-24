package hostmetrics

import (
	"sync"
	"time"
)

// Ring keeps the last hour of host samples (360 at the 10s cadence) in one
// fixed buffer allocated at construction: pushing and window slicing never
// allocate, per ADR-0006's heap budget.
type Ring struct {
	mu     sync.RWMutex
	buf    []HostSample
	next   int // index the next Push writes
	filled int // how many slots hold samples
}

// NewRing constructs a ring holding the given number of samples.
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]HostSample, capacity)}
}

// Push appends a sample, overwriting the oldest once the ring is full.
func (r *Ring) Push(sample HostSample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = sample
	r.next++
	if r.next == len(r.buf) {
		r.next = 0
	}
	if r.filled < len(r.buf) {
		r.filled++
	}
}

// Len reports how many samples the ring holds.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.filled
}

// AppendLast appends the most recent n samples to dst, chronological
// (oldest first). A window larger than the ring's contents returns
// everything it holds. With a dst of sufficient capacity it allocates
// nothing.
func (r *Ring) AppendLast(dst []HostSample, n int) []HostSample {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := n
	if count > r.filled {
		count = r.filled
	}
	if count < 0 {
		count = 0
	}
	for i := r.filled - count; i < r.filled; i++ {
		dst = append(dst, r.buf[r.indexOf(i)])
	}
	return dst
}

// AppendSince appends every sample whose At is not before t, chronological.
// With a dst of sufficient capacity it allocates nothing.
func (r *Ring) AppendSince(dst []HostSample, t time.Time) []HostSample {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := 0; i < r.filled; i++ {
		sample := r.buf[r.indexOf(i)]
		if !sample.At.Before(t) {
			dst = append(dst, sample)
		}
	}
	return dst
}

// indexOf maps a chronological position (0 = oldest held sample) to a slot.
// The single +len(buf) keeps the arithmetic in range when the ring has
// wrapped. Caller holds r.mu.
func (r *Ring) indexOf(i int) int {
	return (r.next - r.filled + i + len(r.buf)) % len(r.buf)
}
