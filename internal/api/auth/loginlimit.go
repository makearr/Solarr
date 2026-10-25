package auth

import (
	"net/netip"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Login attempts allowed from one address within LoginWindow before it must wait.
const (
	LoginAttempts = 5
	LoginWindow   = time.Minute
)

// LoginLimiter counts failed sign-ins per client address, so a password cannot be guessed at speed.
type LoginLimiter struct {
	clock    clock.Clock
	mu       sync.Mutex
	failures map[netip.Addr][]time.Time
	// pending counts attempts begun and not yet ended: they may all fail, so they count against the limit too.
	pending map[netip.Addr]int
}

// NewLoginLimiter starts with no failures recorded.
func NewLoginLimiter(cl clock.Clock) *LoginLimiter {
	return &LoginLimiter{clock: cl, failures: map[netip.Addr][]time.Time{}, pending: map[netip.Addr]int{}}
}

// pendingRetry is the wait answered while the address's other attempts are still being checked.
const pendingRetry = time.Second

// Wait answers how long the address must wait before it may try again; zero means it may try now.
func (l *LoginLimiter) Wait(address netip.Addr) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waitLocked(address)
}

func (l *LoginLimiter) waitLocked(address netip.Addr) time.Duration {
	recent := l.recentLocked(address)
	if len(recent) >= LoginAttempts {
		return recent[0].Add(LoginWindow).Sub(l.clock.Now())
	}
	if len(recent)+l.pending[address] >= LoginAttempts {
		return pendingRetry
	}
	return 0
}

// Begin admits one attempt from the address, or answers how long it must wait. An admitted attempt must be ended
// with End: checking a password takes long enough that attempts sent together would otherwise all be admitted
// before the first failure is recorded.
func (l *LoginLimiter) Begin(address netip.Addr) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait := l.waitLocked(address); wait > 0 {
		return wait
	}
	l.pending[address]++
	return 0
}

// End closes an attempt Begin admitted, recording it as a failure when it failed.
func (l *LoginLimiter) End(address netip.Addr, failed bool) {
	l.mu.Lock()
	if l.pending[address]--; l.pending[address] <= 0 {
		delete(l.pending, address)
	}
	l.mu.Unlock()
	if failed {
		l.Failed(address)
	}
}

// Failed records one failed sign-in from the address.
func (l *LoginLimiter) Failed(address netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.failures) >= sweepAbove {
		for other := range l.failures {
			l.recentLocked(other)
		}
		// A burst from many live addresses would grow the table without end: when full, the oldest failer goes.
		if _, tracked := l.failures[address]; !tracked && len(l.failures) >= sweepAbove {
			var oldestAddress netip.Addr
			var oldest time.Time
			for other, attempts := range l.failures {
				last := attempts[len(attempts)-1]
				if oldest.IsZero() || last.Before(oldest) {
					oldestAddress, oldest = other, last
				}
			}
			delete(l.failures, oldestAddress)
		}
	}
	l.failures[address] = append(l.recentLocked(address), l.clock.Now())
}

// sweepAbove is how many addresses are tracked before stale ones are dropped, so many sources cannot grow it for ever.
const sweepAbove = 1024

// recentLocked drops the address's failures older than the window, and the address once it has none.
func (l *LoginLimiter) recentLocked(address netip.Addr) []time.Time {
	cutoff := l.clock.Now().Add(-LoginWindow)
	recent := l.failures[address]
	for len(recent) > 0 && !recent[0].After(cutoff) {
		recent = recent[1:]
	}
	if len(recent) == 0 {
		delete(l.failures, address)
		return nil
	}
	l.failures[address] = recent
	return recent
}
