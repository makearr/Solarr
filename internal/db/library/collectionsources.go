package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// WorkToCheck is a work a collection source has yet to be asked about, with the id that provider knows it by and the
// label of its first edition ("" without one).
type WorkToCheck struct {
	WorkID       int64
	ExternalID   string
	EditionLabel string
}

// WorksToCheck lists, after a work id, up to limit works of the media type known to the provider that source was not
// asked about within maxAge; maxAge 0 asks each work once.
func (f Franchises) WorksToCheck(ctx context.Context, mediaType, provider, source string, maxAge time.Duration, after int64, limit int) ([]WorkToCheck, error) {
	var since int64
	if maxAge > 0 {
		since = f.store.Now().Add(-maxAge).Unix()
	}
	rows, err := librarydb.New(f.store.Read()).ListWorksToCheckForCollections(ctx, librarydb.ListWorksToCheckForCollectionsParams{
		Provider: provider, MediaType: mediaType, AfterID: after, Source: source, Since: since, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("library: works to ask %s about: %w", source, err)
	}
	out := make([]WorkToCheck, len(rows))
	for index, row := range rows {
		out[index] = WorkToCheck{WorkID: row.WorkID, ExternalID: row.ExternalID, EditionLabel: row.EditionLabel}
	}
	return out, nil
}

// MarkChecked records that source was asked about the works now, a bounded batch per write.
func (f Franchises) MarkChecked(ctx context.Context, source string, workIDs []int64) error {
	for start := 0; start < len(workIDs); start += 200 {
		batch := workIDs[start:min(start+200, len(workIDs))]
		err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, workID := range batch {
				if err := queries.MarkWorkCollectionChecked(ctx, librarydb.MarkWorkCollectionCheckedParams{
					WorkID: workID, Source: source, CheckedAt: f.store.Now().Unix(),
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: mark works asked by %s: %w", source, err)
		}
	}
	return nil
}

// Classifications lists one kind of classification (keywords, lists) of every work of the media type, by work.
func (f Franchises) Classifications(ctx context.Context, mediaType, kind string) (map[int64][]string, error) {
	rows, err := librarydb.New(f.store.Read()).ListClassificationsOfType(ctx, librarydb.ListClassificationsOfTypeParams{MediaType: mediaType, Kind: kind})
	if err != nil {
		return nil, fmt.Errorf("library: %s of %s works: %w", kind, mediaType, err)
	}
	out := map[int64][]string{}
	for _, row := range rows {
		out[row.WorkID] = append(out[row.WorkID], row.Value)
	}
	return out, nil
}

// FranchiseListKind is the classification a work carries for each provider's official franchise list it is on ("tvdb:<id>").
const FranchiseListKind = "franchise_list"

// SetClassificationMembers makes exactly these works carry one classification value: a list read whole names all its titles.
func (f Franchises) SetClassificationMembers(ctx context.Context, kind, value, source string, workIDs []int64) error {
	for start := 0; start == 0 || start < len(workIDs); start += 200 {
		batch := workIDs[start:min(start+200, len(workIDs))]
		err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			if start == 0 {
				if err := queries.DeleteClassificationValue(ctx, librarydb.DeleteClassificationValueParams{Kind: kind, Value: value}); err != nil {
					return err
				}
			}
			for _, workID := range batch {
				if err := queries.SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{WorkID: workID, Kind: kind, Value: value, Source: source}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: works with %s %q: %w", kind, value, err)
		}
	}
	return nil
}

// KeepClassifications takes from a work the values of one kind that are not among keep.
func (f Franchises) KeepClassifications(ctx context.Context, workID int64, kind string, keep []string) error {
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		held, err := queries.ListWorkClassifications(ctx, workID)
		if err != nil {
			return err
		}
		for _, row := range held {
			if row.Kind != kind || slices.Contains(keep, row.Value) {
				continue
			}
			if err := queries.DeleteWorkClassification(ctx, librarydb.DeleteWorkClassificationParams{WorkID: workID, Kind: kind, Value: row.Value}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: keep %s of work %d: %w", kind, workID, err)
	}
	return nil
}
