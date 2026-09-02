// Package clock is the injected time seam (ADR-17) (Invariant 45).
package clock

import (
	"context"
	"time"
)

// Clock supplies the current time and the means to wait for a later one.
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	Until(t time.Time) time.Duration

	// NewTimer fires once after d. Stop it when the waiter goes away.
	NewTimer(d time.Duration) Timer

	// NewTicker fires repeatedly every d. d must be positive.
	NewTicker(d time.Duration) Ticker

	// Sleep waits for d or until ctx ends, returning ctx.Err() if it ended first.
	Sleep(ctx context.Context, d time.Duration) error
}

// Timer is the subset of *time.Timer the codebase uses.
type Timer interface {
	// C never yields more than one value per arming.
	C() <-chan time.Time
	// Stop reports whether the timer was still pending.
	Stop() bool
	// Reset re-arms the timer for d and reports whether it was still pending.
	Reset(d time.Duration) bool
}

// Ticker is the subset of *time.Ticker the codebase uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
	Reset(d time.Duration)
}
