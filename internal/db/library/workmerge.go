package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrMergeRefused is a merge of a work into itself or into a work of another media type.
var ErrMergeRefused = errors.New("library: these two works cannot be merged")

// WorkMerge is what folding one work into another moves: the preview a person confirms, and the apply's answer.
type WorkMerge struct {
	DropID, KeepID       int64
	DropTitle, KeepTitle string
	Files                int64
	WantedItems          int64
	Acquisitions         int64
	ImportUnits          int64
}

// MergeSuggestion is another work this one was found to be (an album held twice).
type MergeSuggestion struct {
	IntoWorkID int64
	Title      string
	LeadName   string
}

// PreviewWorkMerge answers what MergeWork would move, changing nothing.
func (i Identity) PreviewWorkMerge(ctx context.Context, dropID, keepID int64) (WorkMerge, error) {
	return previewWorkMerge(ctx, librarydb.New(i.store.Read()), dropID, keepID)
}

func previewWorkMerge(ctx context.Context, queries *librarydb.Queries, dropID, keepID int64) (WorkMerge, error) {
	if dropID == keepID {
		return WorkMerge{}, ErrMergeRefused
	}
	drop, err := queries.GetWork(ctx, dropID)
	if err != nil {
		return WorkMerge{}, fmt.Errorf("library: merge: work %d: %w", dropID, err)
	}
	keep, err := queries.GetWork(ctx, keepID)
	if err != nil {
		return WorkMerge{}, fmt.Errorf("library: merge: work %d: %w", keepID, err)
	}
	if drop.MediaType != keep.MediaType {
		return WorkMerge{}, ErrMergeRefused
	}
	counts, err := queries.CountWorkMerge(ctx, dropID)
	if err != nil {
		return WorkMerge{}, fmt.Errorf("library: merge: count: %w", err)
	}
	return WorkMerge{
		DropID: dropID, KeepID: keepID, DropTitle: drop.CanonicalTitle, KeepTitle: keep.CanonicalTitle,
		Files: counts.Files, WantedItems: counts.WantedItems, Acquisitions: counts.Acquisitions, ImportUnits: counts.ImportUnits,
	}, nil
}

// MergeWork folds dropID into keepID in one transaction: its files, wanted items (onto the kept work's item for the
// same group where it has one), downloads, imports, requests, tags and ids move over, its title becomes an alias, and
// the dropped work is deleted. A file whose part the kept work lacks is left for the media type to place again.
func (i Identity) MergeWork(ctx context.Context, dropID, keepID int64) (WorkMerge, error) {
	var merged WorkMerge
	err := i.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		var err error
		if merged, err = previewWorkMerge(ctx, queries, dropID, keepID); err != nil {
			return err
		}
		dropMembers, err := membersOfWork(ctx, queries, dropID)
		if err != nil {
			return err
		}
		ids := librarydb.MergeWorkFilesParams{KeepID: keepID, DropID: dropID}
		if err := queries.MergeWorkFiles(ctx, ids); err != nil {
			return fmt.Errorf("files: %w", err)
		}
		if err := queries.MergeWorkRootFolders(ctx, librarydb.MergeWorkRootFoldersParams(ids)); err != nil {
			return fmt.Errorf("root folders: %w", err)
		}
		if err := mergeWantedItems(ctx, queries, dropID, keepID); err != nil {
			return err
		}
		if err := mergeEditions(ctx, queries, dropID, keepID); err != nil {
			return err
		}
		keep := sql.NullInt64{Int64: keepID, Valid: true}
		drop := sql.NullInt64{Int64: dropID, Valid: true}
		for _, move := range []struct {
			name string
			run  func() error
		}{
			{"import units", func() error {
				return queries.MergeWorkImportUnits(ctx, librarydb.MergeWorkImportUnitsParams{KeepID: keep, DropID: dropID})
			}},
			{"pack items", func() error {
				return queries.MergeWorkPackItems(ctx, librarydb.MergeWorkPackItemsParams{KeepID: keep, DropID: dropID})
			}},
			{"*arr imports", func() error {
				return queries.MergeWorkArrImportItems(ctx, librarydb.MergeWorkArrImportItemsParams{KeepID: keep, DropID: drop})
			}},
			{"requests", func() error {
				return queries.MergeWorkRequests(ctx, librarydb.MergeWorkRequestsParams{KeepID: keep, DropID: drop})
			}},
			{"identifications", func() error {
				return queries.MergeWorkManualIdentifications(ctx, librarydb.MergeWorkManualIdentificationsParams(ids))
			}},
			{"tags", func() error { return queries.MergeWorkTags(ctx, librarydb.MergeWorkTagsParams(ids)) }},
		} {
			if err := move.run(); err != nil {
				return fmt.Errorf("%s: %w", move.name, err)
			}
		}
		if err := queries.DeleteWorkTags(ctx, dropID); err != nil {
			return fmt.Errorf("tags: %w", err)
		}
		if err := queries.MergeWorkExternalIDs(ctx, librarydb.MergeWorkExternalIDsParams{
			KeepID: keepID, Confidence: aliasConfidence, DropID: dropID,
		}); err != nil {
			return fmt.Errorf("external ids: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(merged.DropTitle), strings.TrimSpace(merged.KeepTitle)) {
			if _, err := queries.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
				WorkID: keepID, Title: merged.DropTitle, Normalized: strings.ToLower(strings.Join(strings.Fields(merged.DropTitle), " ")),
				Kind: "alias", Source: nullString("merge"),
			}); err != nil {
				return fmt.Errorf("title alias: %w", err)
			}
		}
		if err := queries.DeleteWork(ctx, dropID); err != nil {
			return fmt.Errorf("delete work: %w", err)
		}
		if err := recomputeWorkItemsTx(ctx, queries, keepID, i.store.Now()); err != nil {
			return fmt.Errorf("recount: %w", err)
		}
		if err := dropMembers.refresh(ctx, queries); err != nil {
			return err
		}
		return refreshWorkMembers(ctx, queries, keepID)
	})
	if err != nil {
		return WorkMerge{}, fmt.Errorf("library: merge work %d into %d: %w", dropID, keepID, err)
	}
	return merged, nil
}

// mergeWantedItems moves each of the dropped work's items onto the kept work's item for the same group and kind, or
// over whole where the kept work has none.
func mergeWantedItems(ctx context.Context, queries *librarydb.Queries, dropID, keepID int64) error {
	dropItems, err := queries.ListDesiredItemsForWork(ctx, dropID)
	if err != nil {
		return fmt.Errorf("items: %w", err)
	}
	keepItems, err := queries.ListDesiredItemsForWork(ctx, keepID)
	if err != nil {
		return fmt.Errorf("items: %w", err)
	}
	for _, item := range dropItems {
		counterpart := int64(0)
		for _, kept := range keepItems {
			if kept.MediaRef == item.MediaRef && kept.SatisfactionKind == item.SatisfactionKind {
				counterpart = kept.ID
				break
			}
		}
		if counterpart == 0 {
			if err := queries.MoveDesiredItemToWork(ctx, librarydb.MoveDesiredItemToWorkParams{KeepID: keepID, ItemID: item.ID}); err != nil {
				return fmt.Errorf("move item %d: %w", item.ID, err)
			}
			if err := queries.DropUnplacedItemFiles(ctx, librarydb.DropUnplacedItemFilesParams{ItemID: item.ID, KeepID: keepID}); err != nil {
				return fmt.Errorf("item %d files: %w", item.ID, err)
			}
			continue
		}
		if err := queries.MergeDesiredItemFiles(ctx, librarydb.MergeDesiredItemFilesParams{
			ToItemID: counterpart, FromItemID: item.ID, KeepID: keepID,
		}); err != nil {
			return fmt.Errorf("item %d files: %w", item.ID, err)
		}
		if err := queries.MoveAcquisitionsToItem(ctx, librarydb.MoveAcquisitionsToItemParams{
			ToItemID: sql.NullInt64{Int64: counterpart, Valid: true}, FromItemID: sql.NullInt64{Int64: item.ID, Valid: true},
		}); err != nil {
			return fmt.Errorf("item %d downloads: %w", item.ID, err)
		}
	}
	return nil
}

// mergeEditions moves the dropped work's editions over, folding one the kept work already has under the same name.
func mergeEditions(ctx context.Context, queries *librarydb.Queries, dropID, keepID int64) error {
	editions, err := queries.ListEditions(ctx, dropID)
	if err != nil {
		return fmt.Errorf("editions: %w", err)
	}
	for _, edition := range editions {
		twin, err := queries.FindEditionByLabel(ctx, librarydb.FindEditionByLabelParams{WorkID: keepID, Kind: edition.Kind, Label: edition.Label})
		switch {
		case err == nil:
			if err := foldEdition(ctx, queries, edition.ID, twin); err != nil {
				return fmt.Errorf("fold edition %d: %w", edition.ID, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			if err := queries.MoveEditionToWork(ctx, librarydb.MoveEditionToWorkParams{KeepID: keepID, EditionID: edition.ID}); err != nil {
				return fmt.Errorf("move edition %d: %w", edition.ID, err)
			}
		default:
			return fmt.Errorf("edition twin: %w", err)
		}
	}
	return nil
}

// SuggestWorkMerge records that workID was found to be intoWorkID, for a person to merge.
func (i Identity) SuggestWorkMerge(ctx context.Context, workID, intoWorkID int64) error {
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SuggestWorkMerge(ctx, librarydb.SuggestWorkMergeParams{
			WorkID: workID, IntoWorkID: intoWorkID, FoundAt: i.store.Now().Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: suggest merging work %d into %d: %w", workID, intoWorkID, err)
	}
	return nil
}

// MergeSuggestionFor answers the work workID was found to be; false when none.
func (i Identity) MergeSuggestionFor(ctx context.Context, workID int64) (MergeSuggestion, bool, error) {
	row, err := librarydb.New(i.store.Read()).GetWorkMergeSuggestion(ctx, workID)
	if errors.Is(err, sql.ErrNoRows) {
		return MergeSuggestion{}, false, nil
	}
	if err != nil {
		return MergeSuggestion{}, false, fmt.Errorf("library: merge suggestion for work %d: %w", workID, err)
	}
	return MergeSuggestion{IntoWorkID: row.IntoWorkID, Title: row.CanonicalTitle, LeadName: row.LeadName}, true, nil
}

// WorkMediaType answers a work's media type.
func (i Identity) WorkMediaType(ctx context.Context, workID int64) (string, error) {
	work, err := librarydb.New(i.store.Read()).GetWork(ctx, workID)
	if err != nil {
		return "", fmt.Errorf("library: work %d: %w", workID, err)
	}
	return work.MediaType, nil
}
