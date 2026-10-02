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
		created, err := queries.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
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
