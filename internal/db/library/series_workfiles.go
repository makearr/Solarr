package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// AttachWholeFiles records scanned files against a work with no parts: each belongs to the work and its root and
// covers the work's one item, whose monitoring is left as it is (a person's choice is never undone by a scan). Several
// files cover it as versions of one work. It answers how many files are now attached.
func (s Series) AttachWholeFiles(ctx context.Context, at time.Time, workID, rootFolderID int64, mediaObjectIDs []int64) (int, error) {
	attached := 0
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if rootFolderID != 0 {
			if err := queries.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{WorkID: workID, RootFolderID: rootFolderID, FirstSeenAt: at.Unix()}); err != nil {
				return fmt.Errorf("root folder: %w", err)
			}
		}
		items, err := queries.ListDesiredItemsForWork(ctx, workID)
		if err != nil {
			return fmt.Errorf("list items: %w", err)
		}
		var item *librarydb.DesiredItem
		for i := range items {
			if items[i].SatisfactionKind == "SET" && items[i].MediaRef == "" && !items[i].EditionID.Valid {
				item = &items[i]
				break
			}
		}
		if item == nil {
			return fmt.Errorf("work %d has no wanted item to attach a file to", workID)
		}
		for _, mediaObjectID := range mediaObjectIDs {
			if err := queries.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: workID, MediaObjectID: mediaObjectID, FirstSeenAt: at.Unix()}); err != nil {
				return fmt.Errorf("link file: %w", err)
			}
			if err := queries.AttachFile(ctx, librarydb.AttachFileParams{
				DesiredItemID: item.ID, MediaObjectID: mediaObjectID, Role: "primary", MediaRef: "", AddedAt: at.Unix(),
			}); err != nil {
				return fmt.Errorf("attach file: %w", err)
			}
			attached++
		}
		return recomputeAlbumTx(ctx, queries, item.ID, workID, s.store.Now())
	})
	if err != nil {
		return 0, fmt.Errorf("library: attach whole files: %w", err)
	}
	return attached, nil
}
