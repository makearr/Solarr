package adapters

import (
	"context"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// waitingPageSize bounds one read of a root's unidentified files.
const waitingPageSize = 500

// WaitingDirectories are the folders holding the root's files that wait for review, sorted and without repeats.
func WaitingDirectories(ctx context.Context, unidentified library.Unidentified, rootFolderID int64) ([]string, error) {
	var directories []string
	cursor := paginate.Cursor{}
	for {
		page, err := unidentified.List(ctx, []int64{rootFolderID}, cursor, waitingPageSize)
		if err != nil {
			return nil, err
		}
		for _, row := range page {
			directories = append(directories, path.Dir(row.RelPath))
		}
		if len(page) < waitingPageSize {
			break
		}
		last := page[len(page)-1]
		cursor = paginate.Cursor{Sort: last.FirstSeenAt, ID: last.MediaObjectID}
	}
	slices.Sort(directories)
	return slices.Compact(directories), nil
}
