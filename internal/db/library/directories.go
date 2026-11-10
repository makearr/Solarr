package library

import (
	"context"
	"fmt"
	"path"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// DirectoryRecord is a directory of a root that holds library files, with when one last appeared in it.
type DirectoryRecord struct {
	Rel     string
	Changed int64
}

const directoryPageSize = 200

// DirectoriesOfRoot lists every directory that holds a file of the root, and every directory above it, from the
// library's own records: no disk is read.
func (p Physical) DirectoriesOfRoot(ctx context.Context, rootFolderID int64) ([]DirectoryRecord, error) {
	changed := map[string]int64{}
	var afterID int64
	for {
		rows, err := librarydb.New(p.store.Read()).ListPathsInRoot(ctx, librarydb.ListPathsInRootParams{
			RootFolderID: rootFolderID, AfterID: afterID, PageSize: directoryPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("library: list paths of root %d: %w", rootFolderID, err)
		}
		for _, row := range rows {
			afterID = row.ID
			if row.MissingSince.Valid {
				continue
			}
			for dir := path.Dir(row.RelPath); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
				if row.CreatedAt > changed[dir] {
					changed[dir] = row.CreatedAt
				}
			}
		}
		if len(rows) < directoryPageSize {
			break
		}
	}
	records := make([]DirectoryRecord, 0, len(changed))
	for rel, at := range changed {
		records = append(records, DirectoryRecord{Rel: rel, Changed: at})
	}
	return records, nil
}
