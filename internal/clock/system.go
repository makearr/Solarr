package clock

import (
	"context"
	"time"
)

// System is the real clock. It is the only place in the codebase permitted to
// reach for the time package's wall clock; the forbidigo lint rule enforces that.
type System struct{}

// NewSystem returns the production clock.
func NewSystem() System { return System{} }

func (System) Now() time.Time                   { return time.Now() }
func (System) Since(t time.Time) time.Duration  { return time.Since(t) }
func (System) Until(t time.Time) time.Duration  { return time.Until(t) }
func (System) NewTimer(d time.Duration) Timer   { return systemTimer{time.NewTimer(d)} }
func (System) NewTicker(d time.Duration) Ticker { return systemTicker{time.NewTicker(d)} }

// Sleep uses a timer rather than time.Sleep so cancellation is observed promptly.
func (System) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type systemTimer struct{ t *time.Timer }

func (s systemTimer) C() <-chan time.Time        { return s.t.C }
func (s systemTimer) Stop() bool                 { return s.t.Stop() }
func (s systemTimer) Reset(d time.Duration) bool { return s.t.Reset(d) }

type systemTicker struct{ t *time.Ticker }

func (s systemTicker) C() <-chan time.Time   { return s.t.C }
func (s systemTicker) Stop()                 { s.t.Stop() }
func (s systemTicker) Reset(d time.Duration) { s.t.Reset(d) }
