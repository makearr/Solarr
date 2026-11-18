package batch

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/parallel"
)

// resolveWidth is how many units look their title up at once; the providers' own rate limits pace the calls, placing
// stays serial.
const resolveWidth = 4

// ResolveAll runs resolve for every unit index, a few at a time, and answers once all have finished. resolve writes
// its answer to a slot of its own index; a panic in one leaves that slot empty, which reads as "not found".
func ResolveAll(ctx context.Context, units int, resolve func(ctx context.Context, index int)) {
	parallel.Do(ctx, units, resolveWidth, func(ctx context.Context, index int) error {
		resolve(ctx, index)
		return nil
	})
}
