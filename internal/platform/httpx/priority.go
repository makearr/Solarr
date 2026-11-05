package httpx

import (
	"context"
)

// Priority orders requests waiting on one provider's budget and concurrency: lower goes first.
type Priority int

// The three lanes (user, 2026-11-05): what a person asked for or is looking at, then new content, then background fill.
// Callers with a finer ladder (musicbrainz.Priority) pass their own values between these.
const (
	PriorityUser       Priority = 1
	PriorityFresh      Priority = 2
	PriorityBackground Priority = 4
)

// PriorityUnset is what a request nobody marked waits at: background work is the code that never asks.
const PriorityUnset = PriorityBackground

type priorityKey struct{}

// WithPriority marks every request made with ctx.
func WithPriority(ctx context.Context, priority Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, priority)
}

// WithPriorityAtLeast marks ctx with priority unless it already carries a more urgent one, so a caller's own ladder
// never demotes a request a person's page or a fresh-content job already put in a higher lane.
func WithPriorityAtLeast(ctx context.Context, priority Priority) context.Context {
	if marked, ok := ctx.Value(priorityKey{}).(Priority); ok && marked < priority {
		return ctx
	}
	return WithPriority(ctx, priority)
}

// PriorityFrom answers the priority ctx carries, or PriorityUnset.
func PriorityFrom(ctx context.Context) Priority {
	if priority, ok := ctx.Value(priorityKey{}).(Priority); ok {
		return priority
	}
	return PriorityUnset
}
