package web

import (
	"sync"
	"time"
)

// loginLimiter slows down password guessing on the admin UI: after
// maxFailures wrong passwords from one address inside window, that address
// is refused until the oldest failure leaves the window.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

const (
	maxFailures = 10
	window      = 5 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string][]time.Time{}, now: time.Now}
}

// recent drops failures older than the window. Callers hold the lock.
func (l *loginLimiter) recent(host string) []time.Time {
	cutoff := l.now().Add(-window)
	kept := l.failures[host][:0]
	for _, t := range l.failures[host] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, host)
		return nil
	}
	l.failures[host] = kept
	return kept
}

// blocked returns how long host must wait, or 0 when it may try again.
func (l *loginLimiter) blocked(host string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	fails := l.recent(host)
	if len(fails) < maxFailures {
		return 0
	}
	return fails[0].Add(window).Sub(l.now())
}

// maxHosts bounds the memory a flood of distinct addresses can take.
const maxHosts = 4096

func (l *loginLimiter) fail(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.failures) >= maxHosts {
		for h := range l.failures {
			l.recent(h)
		}
	}
	if len(l.failures) >= maxHosts {
		return
	}
	l.failures[host] = append(l.recent(host), l.now())
}
