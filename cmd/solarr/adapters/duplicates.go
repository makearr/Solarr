package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// Duplicates satisfies apiserver.DuplicateReader over library.Duplicates.
type Duplicates struct{ Store library.Duplicates }

func (d Duplicates) Duplicates(ctx context.Context, rootFolderIDs []int64, limit int) ([]apiserver.DuplicateGroupRow, error) {
	groups, err := d.Store.Groups(ctx, rootFolderIDs, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.DuplicateGroupRow, len(groups))
	for i, group := range groups {
		rows[i].SizeBytes = group.SizeBytes
		for _, copy := range group.Copies {
			row := apiserver.DuplicateCopyRow{MediaObjectID: copy.MediaObjectID}
			for _, path := range copy.Paths {
				row.Paths = append(row.Paths, apiserver.DuplicatePathRow{RootFolderID: path.RootFolderID, RelPath: path.RelPath})
			}
			rows[i].Copies = append(rows[i].Copies, row)
		}
	}
	return rows, nil
}
