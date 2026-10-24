package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// recycleBinSweepBatchSize bounds one page of a sweep's own reads -- ADR-3's
// "bulk work chunks to 200 rows", the same bound every other bulk pass in
// this codebase uses.
const recycleBinSweepBatchSize = 200

// RecycleBinConfig is section 10.8's retention policy -- resolved by the
// caller from config.yml/settings.db rather than read from either by
// RecycleBin itself (see its own doc comment). Zero disables a guard.
type RecycleBinConfig struct {
	MaxBytes         int64
	MaxPercentOfRoot float64
}

// RootCapacity answers one root folder's total size, for the
// MaxPercentOfRoot guard. A caller that does not want that guard passes nil.
type RootCapacity func(rootFolderID int64) (totalBytes int64, ok bool)

// RecycleBinSweepResult tallies what one Sweep did. Errors holds per-entry
// failures -- one bad row must not stop the rest of the sweep, the same
// per-item isolation EnrichJob's own Run already uses.
type RecycleBinSweepResult struct {
	Purged     int
	BytesFreed int64
	Errors     []error
}

// Sweep purges every entry retention no longer allows: past its own
// expires_at first, then -- if a global byte budget is still exceeded --
// the oldest remaining entries regardless of root, then -- per root, if a
// percentage-of-root budget is given -- the oldest entries in whichever
// roots are over it. Each guard runs only when configured (non-zero).
func (q RecycleBin) Sweep(
	ctx context.Context, cfg RecycleBinConfig, capacity RootCapacity, rootFolderIDs []int64,
) (RecycleBinSweepResult, error) {
	var result RecycleBinSweepResult

	if err := q.sweepExpired(ctx, &result); err != nil {
		return result, err
	}
	if cfg.MaxBytes > 0 {
		if err := q.sweepGlobalBudget(ctx, &result, cfg.MaxBytes); err != nil {
			return result, err
		}
	}
	if cfg.MaxPercentOfRoot > 0 && capacity != nil {
		for _, rootFolderID := range rootFolderIDs {
			if err := q.sweepRootBudget(ctx, &result, rootFolderID, cfg.MaxPercentOfRoot, capacity); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (q RecycleBin) sweepExpired(ctx context.Context, result *RecycleBinSweepResult) error {
	now := q.clock.Now().Unix()
	for {
		expired, err := librarydb.New(q.store.Read()).ListExpiredRecycleBin(ctx, librarydb.ListExpiredRecycleBinParams{
			ExpiresAt: now, Limit: recycleBinSweepBatchSize,
		})
		if err != nil {
			return fmt.Errorf("recycle bin: list expired: %w", err)
		}
		if len(expired) == 0 {
			return nil
		}
		before := result.Purged
		q.purgeRecycleBinRows(ctx, result, expired)
		if result.Purged == before {
			return nil // the whole batch failed to purge -- stop rather than retry the same rows forever
		}
		if len(expired) < recycleBinSweepBatchSize {
			return nil
		}
	}
}

func (q RecycleBin) sweepGlobalBudget(ctx context.Context, result *RecycleBinSweepResult, maxBytes int64) error {
	for {
		held, err := librarydb.New(q.store.Read()).SumRecycledBytes(ctx)
		if err != nil {
			return fmt.Errorf("recycle bin: sum bytes: %w", err)
		}
		if held <= maxBytes {
			return nil
		}
		// One at a time, re-checking the budget after each -- a whole batch
		// can easily overshoot a small budget, purging more than necessary.
		oldest, err := librarydb.New(q.store.Read()).ListActiveRecycleBinOldestFirst(ctx, 1)
		if err != nil {
			return fmt.Errorf("recycle bin: list oldest: %w", err)
		}
		if len(oldest) == 0 {
			return nil // held > maxBytes but nothing left to evict -- report and stop, not loop forever
		}
		before := result.Purged
		q.purgeRecycleBinRows(ctx, result, oldest)
		if result.Purged == before {
			return nil // this entry failed to purge -- stop rather than retry it forever
		}
	}
}

func (q RecycleBin) sweepRootBudget(
	ctx context.Context, result *RecycleBinSweepResult, rootFolderID int64, maxPercent float64, capacity RootCapacity,
) error {
	totalBytes, ok := capacity(rootFolderID)
	if !ok || totalBytes <= 0 {
		return nil // no known capacity for this root -- nothing to compare against
	}
	var budget int64
	switch {
	case maxPercent >= 100:
		budget = totalBytes
	case maxPercent <= 0:
		budget = 0
	default:
		fraction := float64(totalBytes) * (maxPercent / 100)
		// For very large int64 totals, float64 rounds the total up to 2^63.
		// Avoid converting that out-of-range value back to int64.
		if fraction >= float64(totalBytes) {
			budget = totalBytes
		} else {
			budget = int64(fraction)
		}
	}

	for {
		held, err := librarydb.New(q.store.Read()).RecycledBytesForRoot(ctx, sql.NullInt64{Int64: rootFolderID, Valid: true})
		if err != nil {
			return fmt.Errorf("recycle bin: sum bytes for root %d: %w", rootFolderID, err)
		}
		if held <= budget {
			return nil
		}
		// One at a time, same reasoning as sweepGlobalBudget.
		oldest, err := librarydb.New(q.store.Read()).ListActiveRecycleBinByRootOldestFirst(ctx,
			librarydb.ListActiveRecycleBinByRootOldestFirstParams{
				RootFolderID: sql.NullInt64{Int64: rootFolderID, Valid: true}, Limit: 1,
			})
		if err != nil {
			return fmt.Errorf("recycle bin: list oldest for root %d: %w", rootFolderID, err)
		}
		if len(oldest) == 0 {
			return nil
		}
		before := result.Purged
		q.purgeRecycleBinRows(ctx, result, oldest)
		if result.Purged == before {
			return nil // this entry failed to purge -- stop rather than retry it forever
		}
	}
}

// purgeRecycleBinRows purges a batch, isolating one entry's failure from the rest.
func (q RecycleBin) purgeRecycleBinRows(ctx context.Context, result *RecycleBinSweepResult, rows []librarydb.RecycleBin) {
	for _, row := range rows {
		if err := q.Purge(ctx, row.ID); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("recycle bin: purge %d: %w", row.ID, err))
			continue
		}
		result.Purged++
		result.BytesFreed += row.SizeBytes
	}
}
