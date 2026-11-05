package audit

import (
	"testing"
	"time"
)

func TestLimiterCapsEachKeyPerWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	for i, want := range []bool{true, true, false} {
		if got := l.Allow("a"); got != want {
			t.Fatalf("a #%d = %v, want %v", i, got, want)
		}
	}
	if !l.Allow("b") {
		t.Fatal("another key shares the cap")
	}
	now = now.Add(time.Minute)
	if !l.Allow("a") {
		t.Fatal("the cap did not reset after the window")
	}
}

func TestLimiterForgetsOldKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return now }
	for i := range maxLimiterKeys {
		l.Allow(string(rune(i)))
	}
	now = now.Add(2 * time.Minute)
	l.Allow("fresh")
	if len(l.windows) != 1 {
		t.Fatalf("windows = %d, want the expired keys dropped", len(l.windows))
	}
}
