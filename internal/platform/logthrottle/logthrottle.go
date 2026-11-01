// Package logthrottle keeps a condition that repeats thousands of times from drowning the log.
package logthrottle

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Key marks a record as one of a repeating kind: only the first of a Key per window is logged.
type Key string

// Keyed is implemented by an error that says which repeating condition it is (a provider's open circuit).
type Keyed interface{ ThrottleKey() string }

// pruneAbove bounds the keys remembered; a stale one is forgotten past it.
const pruneAbove = 1000

type entry struct {
	last    time.Time
	dropped int
}

type state struct {
	clock  clock.Clock
	window time.Duration
	mu     sync.Mutex
	seen   map[string]entry
}

// Handler passes records on, except a repeat of a keyed one inside the window: that is counted, and the next one logged
// says how many were left out.
type Handler struct {
	next  slog.Handler
	state *state
}

// New wraps next, throttling records that carry a Key attribute or a Keyed error.
func New(next slog.Handler, c clock.Clock, window time.Duration) *Handler {
	return &Handler{next: next, state: &state{clock: c, window: window, seen: map[string]entry{}}}
}

// Enabled defers to the wrapped handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle logs the record unless it repeats a keyed one still inside the window.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	key := keyOf(record)
	if key == "" {
		return h.next.Handle(ctx, record)
	}
	dropped, logIt := h.state.admit(key)
	if !logIt {
		return nil
	}
	if dropped > 0 {
		record.AddAttrs(slog.Int("suppressed", dropped))
	}
	return h.next.Handle(ctx, record)
}

// WithAttrs wraps the next handler and shares the throttle.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs), state: h.state}
}

// WithGroup wraps the next handler and shares the throttle.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), state: h.state}
}

func (s *state) admit(key string) (dropped int, logIt bool) {
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	have := s.seen[key]
	if !have.last.IsZero() && now.Sub(have.last) < s.window {
		have.dropped++
		s.seen[key] = have
		return 0, false
	}
	if len(s.seen) > pruneAbove {
		for stale, e := range s.seen {
			if now.Sub(e.last) >= s.window {
				delete(s.seen, stale)
			}
		}
	}
	s.seen[key] = entry{last: now}
	return have.dropped, true
}

func keyOf(record slog.Record) string {
	var key string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Value.Kind() != slog.KindAny {
			return true
		}
		switch value := attr.Value.Any().(type) {
		case Key:
			key = string(value)
		case error:
			var keyed Keyed
			if errors.As(value, &keyed) {
				key = keyed.ThrottleKey()
			}
		}
		return key == ""
	})
	if key == "" {
		return ""
	}
	return record.Message + "|" + key
}
