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
}

// NewLoginLimiter starts with no failures recorded.
func NewLoginLimiter(cl clock.Clock) *LoginLimiter {
	return &LoginLimiter{clock: cl, failures: map[netip.Addr][]time.Time{}}
}

// Wait answers how long the address must wait before it may try again; zero means it may try now.
func (l *LoginLimiter) Wait(address netip.Addr) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recentLocked(address)
	if len(recent) < LoginAttempts {
		return 0
	}
	return recent[0].Add(LoginWindow).Sub(l.clock.Now())
}

// Failed records one failed sign-in from the address.
func (l *LoginLimiter) Failed(address netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.failures) >= sweepAbove {
		for other := range l.failures {
			l.recentLocked(other)
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
