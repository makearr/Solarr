package library

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// FilePath is one place a file is held, and how it came to be there ("hardlink", "reflink", "copy").
type FilePath struct {
	RootFolderID int64
	RelPath      string
	LinkKind     string
	Primary      bool
}

// FileDetail is what an expert wants beside a work's file: every path of it, and the other files that may be the same.
type FileDetail struct {
	MediaObjectID int64
	Paths         []FilePath
	// SameBytes counts other files with the same size and head and tail sample; SamePicture those with the same mean picture hash.
	SameBytes, SamePicture int
}

// FileDetails answers the paths and duplicate candidates of every file of a work, in file order.
func (w Works) FileDetails(ctx context.Context, workID int64) ([]FileDetail, error) {
	queries := librarydb.New(w.store.Read())
	paths, err := queries.ListLivePathsForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: file paths of work %d: %w", workID, err)
	}
	sameBytes, err := queries.ListSameBytesPeersForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: same-bytes peers of work %d: %w", workID, err)
	}
	pictures, err := queries.ListSamePicturePeersForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: same-picture peers of work %d: %w", workID, err)
	}
	holders := map[int64]int{}
	hashOf := map[int64]int64{}
	for _, row := range pictures {
		holders[row.Phash.Int64]++
		hashOf[row.ID] = row.Phash.Int64
	}
	bytesPeers := map[int64]int{}
	for _, row := range sameBytes {
		bytesPeers[row.MediaObjectID] = int(row.Peers)
	}
	var details []FileDetail
	for _, row := range paths {
		if len(details) == 0 || details[len(details)-1].MediaObjectID != row.MediaObjectID {
			detail := FileDetail{MediaObjectID: row.MediaObjectID, SameBytes: bytesPeers[row.MediaObjectID]}
			if hash, hashed := hashOf[row.MediaObjectID]; hashed {
				detail.SamePicture = holders[hash] - 1
			}
			details = append(details, detail)
		}
		last := &details[len(details)-1]
		last.Paths = append(last.Paths, FilePath{RootFolderID: row.RootFolderID, RelPath: row.RelPath, LinkKind: row.LinkKind, Primary: row.IsPrimary == 1})
	}
	return details, nil
}
