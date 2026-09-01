package clock

import (
	"context"
	"sync"
	"time"
)

// Fake is a deterministic clock for tests. Time moves only when Advance or Set
// is called, so scheduling, backoff and lease behaviour are reproducible rather
// than timing-dependent (ADR-17).
//
// Waiters woken by Advance run only after Advance returns, because Advance holds
// the lock they need to re-arm. A test that expects a loop to re-arm and fire
// again must therefore call Advance once per expected firing, using
// BlockUntilWaiters to wait for the loop to be armed again.
type Fake struct {
	mu      sync.Mutex
	changed sync.Cond
	now     time.Time
	waiters []*fakeWaiter
	nextID  uint64
}

type fakeWaiter struct {
	id       uint64
	deadline time.Time
	period   time.Duration // non-zero for tickers
	ch       chan time.Time
	fake     *Fake
}

// NewFake returns a Fake started at t.
func NewFake(t time.Time) *Fake {
	f := &Fake{now: t}
	f.changed.L = &f.mu
	return f
}

// NewFakeAtEpoch returns a Fake at a fixed, arbitrary instant, for tests that
// care about elapsed time rather than a particular date.
func NewFakeAtEpoch() *Fake {
	return NewFake(time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC))
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Since(t time.Time) time.Duration { return f.Now().Sub(t) }
func (f *Fake) Until(t time.Time) time.Duration { return t.Sub(f.Now()) }

// Advance moves time forward by d, firing every waiter that comes due, in order.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advanceTo(f.now.Add(d))
}

// Set moves time to t. Moving backwards is allowed and fires nothing; it exists
// for tests of clock skew and of dates in the past.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.Before(f.now) {
		f.now = t
		f.changed.Broadcast()
		return
	}
	f.advanceTo(t)
}

// advanceTo fires due waiters in deadline order. Caller holds the lock.
func (f *Fake) advanceTo(target time.Time) {
	for {
		next := f.earliestDue(target)
		if next == nil {
			break
		}
		f.now = next.deadline
		select {
		case next.ch <- next.deadline:
		default: // a pending tick is still unread; drop this one, as time.Ticker does
		}
		if next.period > 0 {
			next.deadline = next.deadline.Add(next.period)
		} else {
			f.remove(next.id)
		}
	}
	f.now = target
	f.changed.Broadcast()
}

func (f *Fake) earliestDue(target time.Time) *fakeWaiter {
	var earliest *fakeWaiter
	for _, w := range f.waiters {
		if w.deadline.After(target) {
			continue
		}
		if earliest == nil || w.deadline.Before(earliest.deadline) {
			earliest = w
		}
	}
	return earliest
}

// BlockUntilWaiters blocks until at least n timers, tickers or sleepers are armed.
// Use it before Advance so a test never races the goroutine it is driving.
func (f *Fake) BlockUntilWaiters(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.waiters) < n {
		f.changed.Wait()
	}
}

// Waiters reports how many timers, tickers and sleepers are currently armed.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

func (f *Fake) add(d, period time.Duration) *fakeWaiter {
	f.nextID++
	w := &fakeWaiter{
		id:       f.nextID,
		deadline: f.now.Add(d),
		period:   period,
		ch:       make(chan time.Time, 1),
		fake:     f,
	}
	f.waiters = append(f.waiters, w)
	f.changed.Broadcast()
	return w
}

// remove drops the waiter and reports whether it was still armed. Caller holds the lock.
func (f *Fake) remove(id uint64) bool {
	for i, w := range f.waiters {
		if w.id != id {
			continue
		}
		f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
		f.changed.Broadcast()
		return true
	}
	return false
}

func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fakeTimer{w: f.add(d, 0)}
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fakeTicker{w: f.add(d, d)}
}

func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	f.mu.Lock()
	w := f.add(d, 0)
	f.mu.Unlock()

	select {
	case <-ctx.Done():
		f.mu.Lock()
		f.remove(w.id)
		f.mu.Unlock()
		return ctx.Err()
	case <-w.ch:
		return nil
	}
}

type fakeTimer struct{ w *fakeWaiter }

func (t *fakeTimer) C() <-chan time.Time { return t.w.ch }

func (t *fakeTimer) Stop() bool {
	f := t.w.fake
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.remove(t.w.id)
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	f := t.w.fake
	f.mu.Lock()
	defer f.mu.Unlock()
	wasPending := f.remove(t.w.id)
	t.w.deadline = f.now.Add(d)
	f.waiters = append(f.waiters, t.w)
	f.changed.Broadcast()
	return wasPending
}

type fakeTicker struct{ w *fakeWaiter }

func (t *fakeTicker) C() <-chan time.Time { return t.w.ch }

func (t *fakeTicker) Stop() {
	f := t.w.fake
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remove(t.w.id)
}

func (t *fakeTicker) Reset(d time.Duration) {
	if d <= 0 {
		panic("clock: non-positive interval for Ticker.Reset")
	}
	f := t.w.fake
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remove(t.w.id)
	t.w.deadline = f.now.Add(d)
	t.w.period = d
	f.waiters = append(f.waiters, t.w)
	f.changed.Broadcast()
}
