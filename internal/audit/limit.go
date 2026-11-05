package audit

import (
	"sync"
	"time"
)

// maxLimiterKeys is how many keys a Limiter holds before it drops the ones
// whose window has passed.
const maxLimiterKeys = 4096

// Limiter caps how many events one key (a User and an action, an IP) may
// record per window, so a script repeating a refused request or a wrong
// password cannot fill the Audit log. It is in memory: a restart resets it.
type Limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	now     func() time.Time
	windows map[string]*limitWindow
}

type limitWindow struct {
	start time.Time
	count int
}

// NewLimiter allows max events per key in each window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, windows: map[string]*limitWindow{}}
}

// Allow answers whether key may record one more event now, and counts it.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.windows) >= maxLimiterKeys {
		for k, w := range l.windows {
			if now.Sub(w.start) >= l.window {
				delete(l.windows, k)
			}
		}
	}
	w := l.windows[key]
	if w == nil || now.Sub(w.start) >= l.window {
		l.windows[key] = &limitWindow{start: now, count: 1}
		return true
	}
	if w.count >= l.max {
		return false
	}
	w.count++
	return true
}
