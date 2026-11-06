package requestmetrics

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"
)

func TestQuantileWithinTenPercent(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var h Histogram
	samples := make([]float64, 100000)
	lo, hi := math.Log(1), math.Log(60000)
	for i := range samples {
		ms := math.Exp(lo + rng.Float64()*(hi-lo))
		samples[i] = ms
		h.Observe(time.Duration(ms * float64(time.Millisecond)))
	}
	sort.Float64s(samples)
	for _, q := range []float64{0.5, 0.9, 0.99} {
		exact := samples[int(math.Ceil(q*float64(len(samples))))-1]
		est, ok := h.Quantile(q)
		if !ok {
			t.Fatalf("q=%v: not ok", q)
		}
		if math.Abs(est-exact)/exact > 0.10 {
			t.Errorf("q=%v: estimate %.2f vs exact %.2f", q, est, exact)
		}
	}
}

func TestMergeEqualsObservingAll(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var a, b, all Histogram
	for i := 0; i < 5000; i++ {
		d := time.Duration(rng.Int63n(int64(70 * time.Second)))
		all.Observe(d)
		if i%2 == 0 {
			a.Observe(d)
		} else {
			b.Observe(d)
		}
	}
	a.Merge(&b)
	if a.counts != all.counts {
		t.Fatal("merged counts differ from observing all")
	}
}

func TestQuantileEdges(t *testing.T) {
	var h Histogram
	if _, ok := h.Quantile(0.5); ok {
		t.Fatal("empty histogram must not be ok")
	}
	h.Observe(300 * time.Microsecond)
	if v, ok := h.Quantile(0.5); !ok || v != 1 {
		t.Fatalf("underflow = %v, %v; want 1", v, ok)
	}
	var o Histogram
	o.Observe(90 * time.Second)
	if v, ok := o.Quantile(0.99); !ok || v != 60000 {
		t.Fatalf("overflow = %v, %v; want 60000", v, ok)
	}
}

func TestBinaryRoundTrip(t *testing.T) {
	var empty Histogram
	if got := empty.MarshalBinary(); len(got) != 0 {
		t.Fatalf("empty encodes to %d bytes", len(got))
	}
	var h Histogram
	for i := 0; i < NumBuckets; i++ {
		h.counts[i] = uint64(1000000 + i)
	}
	b := h.MarshalBinary()
	if len(b) > 400 {
		t.Fatalf("full histogram is %d bytes", len(b))
	}
	back, err := UnmarshalHistogram(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.counts != h.counts {
		t.Fatal("round trip differs")
	}
	z, err := UnmarshalHistogram(nil)
	if err != nil || z.Total() != 0 {
		t.Fatalf("empty unmarshal = %v, %v", z.Total(), err)
	}
}

func TestUnmarshalRejectsBadIndex(t *testing.T) {
	if _, err := UnmarshalHistogram([]byte{NumBuckets, 1}); err == nil {
		t.Fatal("want error for index >= NumBuckets")
	}
	if _, err := UnmarshalHistogram([]byte{0x80}); err == nil {
		t.Fatal("want error for truncated varint")
	}
}
