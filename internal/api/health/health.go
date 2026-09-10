// Package health answers the liveness and readiness probes (section 20.2).
package health

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
)

// wedgedQueueDepth is the write-queue depth taken as evidence that the writer has stopped
// making progress (Invariant 2).
const wedgedQueueDepth = 256

// Checker reports on the databases. It holds stores rather than a database
// handle so it can see the write queue, which is where a wedge appears first.
type Checker struct {
	stores []*store.Store
}

// New builds a checker over every database the process opened.
func New(stores ...*store.Store) *Checker { return &Checker{stores: stores} }

// Live reports that the write queues are moving. It performs no I/O.
func (c *Checker) Live(_ context.Context) error {
	for _, opened := range c.stores {
		if depth := opened.QueueDepth(); depth >= wedgedQueueDepth {
			return fmt.Errorf("health: %s write queue has %d operations waiting; the writer is not draining",
				opened.Name(), depth)
		}
	}
	return nil
}

// Ready reports that each database is open and answering.
func (c *Checker) Ready(ctx context.Context) error {
	for _, opened := range c.stores {
		if err := opened.Read().PingContext(ctx); err != nil {
			return fmt.Errorf("health: %s is not answering: %w", opened.Name(), err)
		}
	}
	return nil
}
