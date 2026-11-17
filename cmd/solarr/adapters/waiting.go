package adapters

import (
	"context"
	"math"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// waitingPageSize bounds one read of a root's unidentified files.
const waitingPageSize = 500

// WaitingDirectories are the folders holding the root's files that wait for review, sorted and without repeats. Unless
// force is set, only files whose automatic retry is due at now count; a folder is listed when any of its files is due.
func WaitingDirectories(ctx context.Context, unidentified library.Unidentified, rootFolderID, now int64, force bool) ([]string, error) {
	dueBy := now
	if force {
		dueBy = math.MaxInt64
	}
	var directories []string
	cursor := paginate.Cursor{}
	for {
		page, err := unidentified.ListDue(ctx, rootFolderID, dueBy, cursor, waitingPageSize)
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
