package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// PathsGoneFromDirectory answers the ids of the present paths directly inside a directory (relative to the root, ""
// for the root itself) whose names a complete listing of that directory did not contain.
func (p Physical) PathsGoneFromDirectory(ctx context.Context, rootFolderID int64, directory string, listed []string) ([]int64, error) {
	queries := librarydb.New(p.store.Read())
	type present struct {
		id      int64
		relPath string
	}
	var rows []present
	if directory == "" {
		found, err := queries.PresentPathsAtRootLevel(ctx, rootFolderID)
		if err != nil {
			return nil, fmt.Errorf("library: paths at the root of %d: %w", rootFolderID, err)
		}
		for _, row := range found {
			rows = append(rows, present{row.ID, row.RelPath})
		}
	} else {
		found, err := queries.PresentPathsInDirectory(ctx, librarydb.PresentPathsInDirectoryParams{
			RootFolderID: rootFolderID, Prefix: directory + "/", PrefixEnd: directory + "0",
		})
		if err != nil {
			return nil, fmt.Errorf("library: paths in %q of root %d: %w", directory, rootFolderID, err)
		}
		for _, row := range found {
			if !strings.Contains(strings.TrimPrefix(row.RelPath, directory+"/"), "/") {
				rows = append(rows, present{row.ID, row.RelPath})
			}
		}
	}
	named := make(map[string]bool, len(listed))
	for _, name := range listed {
		named[name] = true
	}
	var gone []int64
	for _, row := range rows {
		if !named[row.relPath] {
			gone = append(gone, row.id)
		}
	}
	return gone, nil
}

// MarkPathsMissing marks paths missing at a moment and brings the counts of the works that lost a file in line.
func (p Physical) MarkPathsMissing(ctx context.Context, rootFolderID int64, ids []int64, at time.Time) (int, error) {
	if err := p.noteMissingRecount(ctx, rootFolderID, at.Unix()); err != nil {
		return 0, err
	}
	total := 0
	for chunk := range slices.Chunk(ids, dirtyBatchSize) {
		var changed int64
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var batchErr error
			changed, batchErr = librarydb.New(tx).MarkLibraryPathsMissingByID(ctx, librarydb.MarkLibraryPathsMissingByIDParams{
				MissingSince: sql.NullInt64{Int64: at.Unix(), Valid: true}, Ids: chunk,
			})
			return batchErr
		})
		if err != nil {
			return total, fmt.Errorf("library: mark paths missing: %w", err)
		}
		total += int(changed)
	}
	return total, p.recountMissing(ctx, rootFolderID, at.Unix())
}
