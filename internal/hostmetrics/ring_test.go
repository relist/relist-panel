package hostmetrics

import (
	"testing"
	"time"
)

func baseTime() time.Time {
	return time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
}

func sampleAt(base time.Time, i int) HostSample {
	return HostSample{At: base.Add(time.Duration(i) * 10 * time.Second)}
}

// The ring keeps the last hour (360 samples at the 10s cadence): after 400
// pushes it holds exactly the newest 360, oldest-first on the way out.
func TestRingKeepsLastHour(t *testing.T) {
	r := NewRing(360)
	for i := 0; i < 400; i++ {
		r.Push(sampleAt(baseTime(), i))
	}
	if got := r.Len(); got != 360 {
		t.Fatalf("Len() = %d, want 360", got)
	}
	hour := r.AppendLast(nil, 360)
	if len(hour) != 360 {
		t.Fatalf("AppendLast(nil, 360) returned %d samples, want 360", len(hour))
	}
	if !hour[0].At.Equal(baseTime().Add(40 * 10 * time.Second)) {
		t.Errorf("oldest kept sample is at %v, want %v", hour[0].At, baseTime().Add(40*10*time.Second))
	}
	if !hour[359].At.Equal(baseTime().Add(399 * 10 * time.Second)) {
		t.Errorf("newest sample is at %v, want %v", hour[359].At, baseTime().Add(399*10*time.Second))
	}
}

func TestRingAppendLastSlicesAnyWindow(t *testing.T) {
	r := NewRing(360)
	for i := 0; i < 400; i++ {
		r.Push(sampleAt(baseTime(), i))
	}

	fifteen := r.AppendLast(nil, 90)
	if len(fifteen) != 90 {
		t.Fatalf("15m window returned %d samples, want 90", len(fifteen))
	}
	if !fifteen[0].At.Equal(baseTime().Add(310 * 10 * time.Second)) {
		t.Errorf("15m window starts at %v, want %v", fifteen[0].At, baseTime().Add(310*10*time.Second))
	}

	everything := r.AppendLast(nil, 1000)
	if len(everything) != 360 {
		t.Errorf("oversized window returned %d samples, want the 360 the ring holds", len(everything))
	}

	partial := r.AppendLast(nil, 3)
	if len(partial) != 3 || !partial[2].At.Equal(baseTime().Add(399*10*time.Second)) {
		t.Errorf("AppendLast(nil, 3) = %v, want the 3 newest samples", partial)
	}
}

func TestRingAppendSince(t *testing.T) {
	r := NewRing(360)
	for i := 0; i < 400; i++ {
		r.Push(sampleAt(baseTime(), i))
	}

	since := r.AppendSince(nil, baseTime().Add(370*10*time.Second))
	if len(since) != 30 {
		t.Fatalf("AppendSince returned %d samples, want 30", len(since))
	}
	if !since[0].At.Equal(baseTime().Add(370 * 10 * time.Second)) {
		t.Errorf("first sample since the boundary is at %v", since[0].At)
	}

	// Everything is at or after the zero time: the first minute close of a
	// process's life sees every sample it has.
	all := r.AppendSince(nil, time.Time{})
	if len(all) != 360 {
		t.Errorf("AppendSince(zero time) returned %d samples, want 360", len(all))
	}
}

// Push and window slicing work on the ring's single fixed buffer: given a
// destination with capacity, neither allocates (ADR-0006's heap budget).
func TestRingDoesNotAllocate(t *testing.T) {
	r := NewRing(360)
	for i := 0; i < 360; i++ {
		r.Push(sampleAt(baseTime(), i))
	}
	dst := make([]HostSample, 0, 360)

	if pushes := testing.AllocsPerRun(100, func() { r.Push(sampleAt(baseTime(), 0)) }); pushes != 0 {
		t.Errorf("Push allocated %f times per run, want 0", pushes)
	}
	if slices := testing.AllocsPerRun(100, func() { _ = r.AppendLast(dst[:0], 360) }); slices != 0 {
		t.Errorf("AppendLast allocated %f times per run, want 0", slices)
	}
}
