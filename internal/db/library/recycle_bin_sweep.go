package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

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
	// MinFreeBytes and MinFreePercent keep a root's free space above a floor, by emptying its bin oldest first (the
	// larger floor counts); they need Free.
	MinFreeBytes   int64
	MinFreePercent float64
	Free           RootFree
}

// RootFree answers one root folder's free and total bytes, for the minimum free space guard.
type RootFree func(rootFolderID int64) (freeBytes, totalBytes int64, ok bool)

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

// Sweep purges entries past expires_at, then the oldest across roots if the global byte budget is exceeded, then the oldest per root over a percentage budget; each guard runs only when configured.
func (q RecycleBin) Sweep(
	ctx context.Context, cfg RecycleBinConfig, capacity RootCapacity, rootFolderIDs []int64,
) (RecycleBinSweepResult, error) {
	var result RecycleBinSweepResult
	// Entries that could not be purged this run (an I/O error, a root that is offline) are passed over, not retried
	// and not allowed to hold up the entries behind them.
	skipped := map[int64]bool{}

	if err := q.sweepExpired(ctx, &result, skipped); err != nil {
		return result, err
	}
	if cfg.MaxBytes > 0 {
		if err := q.sweepGlobalBudget(ctx, &result, skipped, cfg.MaxBytes); err != nil {
			return result, err
		}
	}
	if cfg.MaxPercentOfRoot > 0 && capacity != nil {
		for _, rootFolderID := range rootFolderIDs {
			if err := q.sweepRootBudget(ctx, &result, skipped, rootFolderID, cfg.MaxPercentOfRoot, capacity); err != nil {
				return result, err
			}
		}
	}
	if (cfg.MinFreeBytes > 0 || cfg.MinFreePercent > 0) && cfg.Free != nil {
		for _, rootFolderID := range rootFolderIDs {
			if err := q.sweepRootFloor(ctx, &result, skipped, rootFolderID, cfg); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

// freeFloor is the free space a root must have: the larger of the byte floor and the percentage of the root.
func freeFloor(minBytes int64, minPercent float64, totalBytes int64) int64 {
	floor := minBytes
	if minPercent > 0 && totalBytes > 0 {
		if byPercent := int64(float64(totalBytes) / 100 * min(minPercent, 100)); byPercent > floor {
			floor = byPercent
		}
	}
	return floor
}

// sweepRootFloor empties a root's bin, oldest first, until the root has its minimum free space or the bin is empty.
func (q RecycleBin) sweepRootFloor(
	ctx context.Context, result *RecycleBinSweepResult, skipped map[int64]bool, rootFolderID int64, cfg RecycleBinConfig,
) error {
	free, total, ok := cfg.Free(rootFolderID)
	if !ok {
		return nil
	}
	floor := freeFloor(cfg.MinFreeBytes, cfg.MinFreePercent, total)
	for free < floor {
		oldest, err := q.oldestOfRoot(ctx, rootFolderID, skipped)
		if err != nil {
			return err
		}
		if oldest == nil {
			return nil
		}
		before := result.BytesFreed
		q.purgeRecycleBinRows(ctx, result, skipped, []librarydb.RecycleBin{*oldest})
		free += result.BytesFreed - before
	}
	return nil
}

func (q RecycleBin) sweepExpired(ctx context.Context, result *RecycleBinSweepResult, skipped map[int64]bool) error {
	now := q.clock.Now().Unix()
	for {
		expired, err := librarydb.New(q.store.Read()).ListExpiredRecycleBin(ctx, librarydb.ListExpiredRecycleBinParams{
			ExpiresAt: now, Limit: recycleBinSweepBatchSize + int64(len(skipped)),
		})
		if err != nil {
			return fmt.Errorf("recycle bin: list expired: %w", err)
		}
		expired = slices.DeleteFunc(expired, func(row librarydb.RecycleBin) bool { return skipped[row.ID] })
		if len(expired) == 0 {
			return nil
		}
		q.purgeRecycleBinRows(ctx, result, skipped, expired)
	}
}

func (q RecycleBin) sweepGlobalBudget(ctx context.Context, result *RecycleBinSweepResult, skipped map[int64]bool, maxBytes int64) error {
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
		oldest, err := q.oldestOfRoot(ctx, 0, skipped)
		if err != nil {
			return err
		}
		if oldest == nil {
			return nil // held > maxBytes but nothing left to evict -- report and stop, not loop forever
		}
		q.purgeRecycleBinRows(ctx, result, skipped, []librarydb.RecycleBin{*oldest})
	}
}

// oldestOfRoot is the oldest active entry of the root (every root for zero) that this run has not already passed over.
func (q RecycleBin) oldestOfRoot(ctx context.Context, rootFolderID int64, skipped map[int64]bool) (*librarydb.RecycleBin, error) {
	reader := librarydb.New(q.store.Read())
	limit := int64(len(skipped)) + 1
	var rows []librarydb.RecycleBin
	var err error
	if rootFolderID == 0 {
		rows, err = reader.ListActiveRecycleBinOldestFirst(ctx, limit)
	} else {
		rows, err = reader.ListActiveRecycleBinByRootOldestFirst(ctx, librarydb.ListActiveRecycleBinByRootOldestFirstParams{
			RootFolderID: sql.NullInt64{Int64: rootFolderID, Valid: true}, Limit: limit,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("recycle bin: list oldest: %w", err)
	}
	for _, row := range rows {
		if !skipped[row.ID] {
			return &row, nil
		}
	}
	return nil, nil
}

func (q RecycleBin) sweepRootBudget(
	ctx context.Context, result *RecycleBinSweepResult, skipped map[int64]bool, rootFolderID int64, maxPercent float64, capacity RootCapacity,
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
		oldest, err := q.oldestOfRoot(ctx, rootFolderID, skipped)
		if err != nil {
			return err
		}
		if oldest == nil {
			return nil
		}
		q.purgeRecycleBinRows(ctx, result, skipped, []librarydb.RecycleBin{*oldest})
	}
}

// purgeRecycleBinRows purges a batch, isolating one entry's failure from the rest. A failure or an offline root puts
// the entry in skipped for the rest of the run; only a failure is an error.
func (q RecycleBin) purgeRecycleBinRows(ctx context.Context, result *RecycleBinSweepResult, skipped map[int64]bool, rows []librarydb.RecycleBin) {
	for _, row := range rows {
		err := q.Purge(ctx, row.ID)
		switch {
		case errors.Is(err, ErrRootOffline):
			skipped[row.ID] = true
		case err != nil:
			skipped[row.ID] = true
			result.Errors = append(result.Errors, fmt.Errorf("recycle bin: purge %d: %w", row.ID, err))
		default:
			result.Purged++
			result.BytesFreed += row.SizeBytes
		}
	}
}
