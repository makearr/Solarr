package library

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Duplicates finds files that are likely copies of one another: same size and same sampled content (quick_hash).
// It only reads; linking what it finds is a separate, previewed action.
type Duplicates struct{ store *store.Store }

// NewDuplicates builds it over an open library store.
func NewDuplicates(opened *store.Store) Duplicates { return Duplicates{store: opened} }

// DuplicatePath is one place a copy lives.
type DuplicatePath struct {
	RootFolderID int64
	RelPath      string
}

// DuplicateCopy is one stored file, which may be reachable by several paths (linked, or in two libraries).
type DuplicateCopy struct {
	MediaObjectID int64
	Paths         []DuplicatePath
}

// DuplicateGroup is files believed to be one file stored more than once.
type DuplicateGroup struct {
	SizeBytes int64
	Copies    []DuplicateCopy
}

// Reclaimable is what keeping one copy would free.
func (g DuplicateGroup) Reclaimable() int64 { return g.SizeBytes * int64(len(g.Copies)-1) }

// Groups answers the biggest duplicate groups with at least two stored files under rootFolderIDs, only counting
// paths under them. It looks at the library's largest limit groups, so a small library's list is complete and a
// huge one's is the part worth acting on first.
func (d Duplicates) Groups(ctx context.Context, rootFolderIDs []int64, limit int) ([]DuplicateGroup, error) {
	inRoots := make(map[int64]bool, len(rootFolderIDs))
	for _, id := range rootFolderIDs {
		inRoots[id] = true
	}
	reader := librarydb.New(d.store.Read())
	candidates, err := reader.ListDuplicateGroups(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("library: duplicate groups: %w", err)
	}
	var groups []DuplicateGroup
	for _, candidate := range candidates {
		members, err := reader.ListDuplicateMembers(ctx, librarydb.ListDuplicateMembersParams{
			QuickHash: candidate.QuickHash, SizeBytes: candidate.SizeBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("library: duplicate members: %w", err)
		}
		group := DuplicateGroup{SizeBytes: candidate.SizeBytes}
		for _, member := range members {
			if !inRoots[member.RootFolderID] {
				continue
			}
			path := DuplicatePath{RootFolderID: member.RootFolderID, RelPath: member.RelPath}
			if last := len(group.Copies) - 1; last >= 0 && group.Copies[last].MediaObjectID == member.MediaObjectID {
				group.Copies[last].Paths = append(group.Copies[last].Paths, path)
				continue
			}
			group.Copies = append(group.Copies, DuplicateCopy{MediaObjectID: member.MediaObjectID, Paths: []DuplicatePath{path}})
		}
		if len(group.Copies) >= 2 {
			groups = append(groups, group)
		}
	}
	return groups, nil
}
