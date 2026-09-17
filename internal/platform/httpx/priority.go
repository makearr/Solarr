package httpx

import (
	"context"
	"math"
)

// Priority orders requests waiting on one provider's budget: lower goes first. A request
// carrying none waits behind every request that carries one.
type Priority int

// PriorityUnset is what a request without a priority waits at.
const PriorityUnset Priority = math.MaxInt32

type priorityKey struct{}

// WithPriority marks every request made with ctx.
func WithPriority(ctx context.Context, priority Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, priority)
}

// PriorityFrom answers the priority ctx carries, or PriorityUnset.
func PriorityFrom(ctx context.Context) Priority {
	if priority, ok := ctx.Value(priorityKey{}).(Priority); ok {
		return priority
	}
	return PriorityUnset
}
