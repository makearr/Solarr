package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// partInScope reports whether a part belongs to a SET item: an item with no media_ref covers the whole work (an
// album), one with a media_ref covers the parts below it, written "<item ref>:<part>" ("s03" covers "s03:e10").
func partInScope(partRef, itemRef string) bool {
	return itemRef == "" || strings.HasPrefix(partRef, itemRef+":")
}

// SeasonRequest identifies, and if needed creates, a SET item for one part of a work: a TV season, a comic arc.
type SeasonRequest struct {
	WorkID     int64
	MediaRef   string
	SortKey    string
	CategoryID int64
	ProfileID  int64
	Monitored  bool
	AddedBy    *int64
}

// EnsureSeasonItem finds the work's SET item for request.MediaRef or creates it, then derives its counts from the
// parts in its scope. An item that exists keeps its own monitored flag and profile (Invariant 71).
func (d Desired) EnsureSeasonItem(ctx context.Context, request SeasonRequest) (librarydb.DesiredItem, error) {
	var item librarydb.DesiredItem
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.ListDesiredItemsForWork(ctx, request.WorkID)
		if err != nil {
			return fmt.Errorf("list existing: %w", err)
		}
		for _, candidate := range existing {
			if candidate.SatisfactionKind == "SET" && candidate.MediaRef == request.MediaRef {
				item = candidate
				return recomputeAlbumTx(ctx, queries, item.ID, request.WorkID)
			}
		}
		created, err := createDesiredItem(ctx, queries, librarydb.CreateDesiredItemParams{
			WorkID: request.WorkID, MediaRef: request.MediaRef, MediaSortKey: request.SortKey,
			CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitored: boolToInt(request.Monitored),
			SatisfactionKind: "SET", PresentCount: 0, State: "MISSING", Dirty: 1, AddedBy: nullInt64(request.AddedBy),
		})
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		if err := recomputeAlbumTx(ctx, queries, created.ID, request.WorkID); err != nil {
			return fmt.Errorf("derive required count: %w", err)
		}
		item, err = queries.GetDesiredItem(ctx, created.ID)
		return err
	})
	if err != nil {
		return librarydb.DesiredItem{}, fmt.Errorf("library: ensure season item: %w", err)
	}
	return item, nil
}

// SetSeasonMonitored sets the monitored flag of a season item and of every part in its scope in one transaction. It
// reports false when the work has no such season item.
func (d Desired) SetSeasonMonitored(ctx context.Context, workID int64, seasonRef string, monitored bool) (bool, error) {
	found := false
	err := d.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		items, err := queries.ListDesiredItemsForWork(ctx, workID)
		if err != nil {
			return fmt.Errorf("list items: %w", err)
		}
		for _, item := range items {
			if item.SatisfactionKind != "SET" || item.MediaRef != seasonRef {
				continue
			}
			found = true
			if err := queries.SetSeasonPartsMonitored(ctx, librarydb.SetSeasonPartsMonitoredParams{
				Monitored: boolToInt(monitored), WorkID: workID, Scope: seasonRef + ":%",
			}); err != nil {
				return fmt.Errorf("set parts: %w", err)
			}
			if err := queries.SetDesiredItemMonitored(ctx, librarydb.SetDesiredItemMonitoredParams{Monitored: boolToInt(monitored), ID: item.ID}); err != nil {
				return fmt.Errorf("set item: %w", err)
			}
			return recomputeAlbumTx(ctx, queries, item.ID, workID)
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("library: set season monitored: %w", err)
	}
	return found, nil
}

// ApplyPartMonitoring sets the monitored flag of the given parts (part id to wanted) and then every season item's flag
// from what is monitored in its scope, one transaction per season. A season with nothing left wanted is unmonitored,
// one with anything wanted is monitored, so the planner never ignores a wanted episode of an unmonitored season.
func (d Desired) ApplyPartMonitoring(ctx context.Context, workID int64, wanted map[int64]bool) error {
	items, err := librarydb.New(d.store.Read()).ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return fmt.Errorf("library: apply monitoring: %w", err)
	}
	for _, item := range items {
		if item.SatisfactionKind != "SET" || item.MediaRef == "" {
			continue
		}
		err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			parts, err := queries.ListWorkParts(ctx, workID)
			if err != nil {
				return fmt.Errorf("list parts: %w", err)
			}
			monitored := 0
			for _, part := range parts {
				if !partInScope(part.MediaRef, item.MediaRef) {
					continue
				}
				want, asked := wanted[part.ID]
				if !asked {
					want = part.Monitored == 1
				}
				if asked && want != (part.Monitored == 1) {
					if err := queries.SetWorkPartMonitored(ctx, librarydb.SetWorkPartMonitoredParams{Monitored: boolToInt(want), ID: part.ID}); err != nil {
						return fmt.Errorf("set part: %w", err)
					}
				}
				if want {
					monitored++
				}
			}
			if err := queries.SetDesiredItemMonitored(ctx, librarydb.SetDesiredItemMonitoredParams{Monitored: boolToInt(monitored > 0), ID: item.ID}); err != nil {
				return fmt.Errorf("set item: %w", err)
			}
			return recomputeAlbumTx(ctx, queries, item.ID, workID)
		})
		if err != nil {
			return fmt.Errorf("library: apply monitoring to %s: %w", item.MediaRef, err)
		}
	}
	return nil
}
