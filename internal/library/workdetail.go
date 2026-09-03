package library

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// EditionDetail is one edition with the external ids recorded against it.
type EditionDetail struct {
	Edition     librarydb.Edition
	ExternalIDs []librarydb.EditionExternalID
}

// PartDetail is one work_part with the external ids recorded against it.
type PartDetail struct {
	Part        librarydb.WorkPart
	ExternalIDs []librarydb.WorkPartExternalID
}

// WorkDetail is the read-only structural view of a work: its own fields, its
// editions, its parts, and every level's external ids. RootFolderIDs is not
// display data -- it is what the caller uses to run the ADR-27 visibility
// check the same way ListWorks does (Invariant 37).
type WorkDetail struct {
	Work          librarydb.Work
	ExternalIDs   []librarydb.WorkExternalID
	Editions      []EditionDetail
	Parts         []PartDetail
	RootFolderIDs []int64
}

// Detail composes one work's full structural detail. A missing id surfaces
// sql.ErrNoRows through GetWork, wrapped: callers use errors.Is to detect it.
func (w Works) Detail(ctx context.Context, id int64) (WorkDetail, error) {
	queries := librarydb.New(w.store.Read())

	work, err := queries.GetWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: get work: %w", err)
	}

	externalIDs, err := queries.ListWorkExternalIDs(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: work external ids: %w", err)
	}

	editions, err := queries.ListEditions(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: editions: %w", err)
	}
	editionDetails := make([]EditionDetail, len(editions))
	for index, edition := range editions {
		editionExternalIDs, err := queries.ListEditionExternalIDs(ctx, edition.ID)
		if err != nil {
			return WorkDetail{}, fmt.Errorf("library: work detail: edition external ids: %w", err)
		}
		editionDetails[index] = EditionDetail{Edition: edition, ExternalIDs: editionExternalIDs}
	}

	parts, err := queries.ListWorkParts(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: parts: %w", err)
	}
	partDetails := make([]PartDetail, len(parts))
	for index, part := range parts {
		partExternalIDs, err := queries.ListWorkPartExternalIDs(ctx, part.ID)
		if err != nil {
			return WorkDetail{}, fmt.Errorf("library: work detail: part external ids: %w", err)
		}
		partDetails[index] = PartDetail{Part: part, ExternalIDs: partExternalIDs}
	}

	rootFolderIDs, err := queries.ListRootFoldersForWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: root folders: %w", err)
	}

	return WorkDetail{
		Work: work, ExternalIDs: externalIDs, Editions: editionDetails,
		Parts: partDetails, RootFolderIDs: rootFolderIDs,
	}, nil
}
