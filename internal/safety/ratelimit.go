package safety

import (
	"fmt"
	"sync"
	"time"
)

// RateLimiter is a sliding-window in-process rate limiter.
// Check returns 0 if the call is allowed (and records it), or seconds-to-wait
// if blocked (and does NOT record it). Mirrors tgcli.safety.RateLimiter.
type RateLimiter struct {
	max    int
	window time.Duration

	mu     sync.Mutex
	events []time.Time
	now    func() time.Time
}

func NewRateLimiter(maxPerWindow int, window time.Duration) *RateLimiter {
	return &RateLimiter{max: maxPerWindow, window: window, now: time.Now}
}

// SetClock is for tests.
func (r *RateLimiter) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// Check returns the seconds-to-wait, or 0 if allowed (in which case the call
// is recorded).
func (r *RateLimiter) Check() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	cutoff := now.Add(-r.window)
	// Drop expired events from the front.
	i := 0
	for ; i < len(r.events); i++ {
		if r.events[i].After(cutoff) {
			break
		}
	}
	r.events = r.events[i:]
	if len(r.events) >= r.max {
		wait := r.window - now.Sub(r.events[0])
		return wait.Seconds()
	}
	r.events = append(r.events, now)
	return 0
}

// CheckOrError returns nil if allowed, or *LocalRateLimited if blocked.
func (r *RateLimiter) CheckOrError() error {
	if wait := r.Check(); wait > 0 {
		return &LocalRateLimited{
			Msg:               fmt.Sprintf("local rate limit: retry after %.1fs", wait),
			RetryAfterSeconds: wait,
		}
	}
	return nil
}

// OutboundWriteLimiter is the shared write limiter (20 / 60s) matching Python.
var OutboundWriteLimiter = NewRateLimiter(20, 60*time.Second)

// RapidSendWatcher returns a warning string once a threshold is reached.
type RapidSendWatcher struct {
	threshold int
	window    time.Duration

	mu     sync.Mutex
	events []time.Time
	now    func() time.Time
}

func NewRapidSendWatcher(threshold int, window time.Duration) *RapidSendWatcher {
	return &RapidSendWatcher{threshold: threshold, window: window, now: time.Now}
}

// CheckAndWarn returns a warning string when the threshold is reached, else "".
func (w *RapidSendWatcher) CheckAndWarn() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	cutoff := now.Add(-w.window)
	i := 0
	for ; i < len(w.events); i++ {
		if w.events[i].After(cutoff) {
			break
		}
	}
	w.events = w.events[i:]
	w.events = append(w.events, now)
	if len(w.events) >= w.threshold {
		return fmt.Sprintf(
			"rapid send detected: %d writes in last %ds; risk of FloodWait",
			len(w.events), int(w.window.Seconds()),
		)
	}
	return ""
}
