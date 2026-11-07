// Package requestmetrics turns Traefik's Access log into request metrics:
// counts by status class and latency percentiles per minute and per hour
// (ADR-0035).
package requestmetrics

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// NumBuckets is the size of a latency histogram: index 0 is the underflow
// (< 1 ms), 1..61 are log-scale buckets of ratio 1.2, 62 is the overflow
// (>= 60 s).
const NumBuckets = 63

const (
	bucketRatio    = 1.2
	maxMs          = 60000.0
	overflowBucket = NumBuckets - 1
)

var logRatio = math.Log(bucketRatio)

// Histogram counts request durations in log-scale buckets. Merging two
// histograms adds their buckets, so a percentile of a merge is exact to the
// bucket; percentiles are never averaged.
type Histogram struct {
	counts [NumBuckets]uint64
}

// Observe records one duration.
func (h *Histogram) Observe(d time.Duration) {
	h.counts[bucketOf(d)]++
}

func bucketOf(d time.Duration) int {
	ms := float64(d) / float64(time.Millisecond)
	switch {
	case ms < 1:
		return 0
	case ms >= maxMs:
		return overflowBucket
	}
	i := 1 + int(math.Floor(math.Log(ms)/logRatio))
	if i > overflowBucket-1 {
		i = overflowBucket - 1
	}
	return i
}

// Merge adds o's counts to h.
func (h *Histogram) Merge(o *Histogram) {
	for i, c := range o.counts {
		h.counts[i] += c
	}
}

// Total is the number of observations.
func (h *Histogram) Total() uint64 {
	var n uint64
	for _, c := range h.counts {
		n += c
	}
	return n
}

// Quantile returns the geometric midpoint of the bucket holding the q-th
// quantile, in milliseconds; ok is false when the histogram is empty.
// Underflow reports 1 ms and overflow 60 s.
func (h *Histogram) Quantile(q float64) (ms float64, ok bool) {
	total := h.Total()
	if total == 0 {
		return 0, false
	}
	rank := uint64(math.Ceil(q * float64(total)))
	if rank < 1 {
		rank = 1
	}
	var cum uint64
	for i, c := range h.counts {
		cum += c
		if cum < rank {
			continue
		}
		switch i {
		case 0:
			return 1, true
		case overflowBucket:
			return maxMs, true
		}
		return math.Pow(bucketRatio, float64(i-1)) * math.Sqrt(bucketRatio), true
	}
	return maxMs, true
}

// MarshalBinary encodes the non-zero buckets as (index, count) uvarint
// pairs. An empty histogram encodes to nothing.
func (h *Histogram) MarshalBinary() []byte {
	var out []byte
	for i, c := range h.counts {
		if c == 0 {
			continue
		}
		out = binary.AppendUvarint(out, uint64(i))
		out = binary.AppendUvarint(out, c)
	}
	return out
}

// UnmarshalHistogram decodes MarshalBinary's output.
func UnmarshalHistogram(b []byte) (Histogram, error) {
	var h Histogram
	for len(b) > 0 {
		idx, n := binary.Uvarint(b)
		if n <= 0 {
			return Histogram{}, errors.New("histogram: bad bucket index")
		}
		b = b[n:]
		cnt, n := binary.Uvarint(b)
		if n <= 0 {
			return Histogram{}, errors.New("histogram: bad bucket count")
		}
		b = b[n:]
		if idx >= NumBuckets {
			return Histogram{}, errors.New("histogram: bucket index out of range")
		}
		h.counts[idx] += cnt
	}
	return h, nil
}
