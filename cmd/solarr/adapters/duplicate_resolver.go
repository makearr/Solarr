package adapters

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/linker"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// duplicateRetain is how long a copy given up for a link stays restorable.
const duplicateRetain = 7 * 24 * time.Hour

// DuplicateResolver satisfies apiserver.DuplicateResolver: it keeps one copy of a track and either links the others'
// paths to it or recycles them.
type DuplicateResolver struct {
	FS       fsx.FS
	Physical library.Physical
	Recycle  library.RecycleBin
	Recycler RemoveAndBlock
	Settings *settings.Cache
}

type duplicatePath struct {
	id, rootID int64
	rel, abs   string
}

func (d DuplicateResolver) ResolveDuplicates(ctx context.Context, categoryID, keepID int64, drops []apiserver.DuplicateDrop) (apiserver.DuplicateOutcome, error) {
	var outcome apiserver.DuplicateOutcome
	roots := map[int64]settings.RootFolder{}
	for _, root := range d.Settings.Snapshot().RootFoldersForCategory(categoryID) {
		roots[root.ID] = root
	}
	keepPaths, err := d.paths(ctx, keepID, roots, false)
	if err != nil {
		return outcome, err
	}
	if len(keepPaths) == 0 {
		return outcome, fmt.Errorf("%w: the copy to keep is not in this library", apiserver.ErrInvalidChoice)
	}
	for _, drop := range drops {
		if drop.MediaObjectID == keepID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		freed, err := d.resolveOne(ctx, keepID, keepPaths, drop, roots, &outcome)
		if err != nil {
			outcome.Problems = append(outcome.Problems, fmt.Sprintf("copy %d: %v", drop.MediaObjectID, err))
			continue
		}
		outcome.FreedBytes += freed
	}
	return outcome, nil
}

// paths lists a media object's paths, all of which must lie on this library's roots and, when they are to be changed,
// on roots that are online, writable and not read-only.
func (d DuplicateResolver) paths(ctx context.Context, mediaObjectID int64, roots map[int64]settings.RootFolder, change bool) ([]duplicatePath, error) {
	rows, err := d.Physical.PathsForObject(ctx, mediaObjectID)
	if err != nil {
		return nil, err
	}
	var paths []duplicatePath
	for _, row := range rows {
		root, inLibrary := roots[row.RootFolderID]
		if !inLibrary {
			return nil, fmt.Errorf("%w: a path of file %d is outside this library", apiserver.ErrInvalidChoice, mediaObjectID)
		}
		if change && (!root.Online || !root.Writable || root.ReadOnly) {
			return nil, fmt.Errorf("%w: %s is offline, not writable or read-only", apiserver.ErrInvalidChoice, root.Path)
		}
		paths = append(paths, duplicatePath{id: row.ID, rootID: row.RootFolderID, rel: row.RelPath, abs: path.Join(root.Path, row.RelPath)})
	}
	return paths, nil
}

func (d DuplicateResolver) resolveOne(
	ctx context.Context, keepID int64, keepPaths []duplicatePath, drop apiserver.DuplicateDrop,
	roots map[int64]settings.RootFolder, outcome *apiserver.DuplicateOutcome,
) (int64, error) {
	dropPaths, err := d.paths(ctx, drop.MediaObjectID, roots, true)
	if err != nil {
		return 0, err
	}
	if len(dropPaths) == 0 {
		return 0, errors.New("it has no file in this library")
	}
	var size int64
	if info, err := d.FS.Stat(dropPaths[0].abs); err == nil {
		size = info.Size
	}
	switch drop.Action {
	case "recycle":
		if _, err := d.Recycler.RemoveAndBlockFile(ctx, drop.MediaObjectID, apiserver.RemoveAndBlockInput{
			RecycleBin: true, RecycleReason: string(library.ReasonUserDelete), RecycleRetainHours: int(duplicateRetain / time.Hour), Reopen: true,
		}); err != nil {
			return 0, err
		}
		outcome.Recycled++
		return size * int64(len(dropPaths)), nil
	case "link":
		return d.link(ctx, keepID, keepPaths, drop.MediaObjectID, dropPaths, roots, outcome)
	}
	return 0, fmt.Errorf("%w: unknown action %q", apiserver.ErrInvalidChoice, drop.Action)
}

// link replaces each path of the dropped copy with a link to a path of the kept one of the same extension, the old
// file going to the recycle bin first, then records the paths under the kept media object.
func (d DuplicateResolver) link(
	ctx context.Context, keepID int64, keepPaths []duplicatePath, dropID int64, dropPaths []duplicatePath,
	roots map[int64]settings.RootFolder, outcome *apiserver.DuplicateOutcome,
) (int64, error) {
	sources := make([]duplicatePath, len(dropPaths))
	for i, target := range dropPaths {
		source, found := sourceFor(keepPaths, target)
		if !found {
			return 0, fmt.Errorf("%s is a %s file and the kept copy has none of that kind, so it cannot be linked; recycle it instead",
				path.Base(target.rel), strings.ToLower(path.Ext(target.rel)))
		}
		sources[i] = source
	}
	var size int64
	kinds := map[int64]string{}
	for i, target := range dropPaths {
		info, statErr := d.FS.Stat(target.abs)
		entry, err := d.Recycle.Put(ctx, library.PutRequest{
			RootPath: roots[target.rootID].Path, OriginalPath: target.abs, RootFolderID: &target.rootID, MediaObjectID: &dropID,
			Reason: library.ReasonReplaced, Retain: duplicateRetain,
		})
		if err != nil {
			return 0, fmt.Errorf("recycle %s: %w", path.Base(target.rel), err)
		}
		method, err := linker.Make(d.FS, sources[i].abs, target.abs)
		if err != nil {
			// Put the file back where it was: nothing may be lost to a link that could not be made.
			if _, restoreErr := d.Recycle.Restore(ctx, entry.ID); restoreErr != nil {
				return 0, fmt.Errorf("link %s: %w (and it could not be put back: %w)", path.Base(target.rel), err, restoreErr)
			}
			return 0, fmt.Errorf("link %s: %w", path.Base(target.rel), err)
		}
		kinds[target.id] = string(method)
		if statErr == nil {
			size += info.Size
		}
	}
	works, err := d.Physical.MergeObjectInto(ctx, keepID, dropID, kinds)
	if err != nil {
		return 0, err
	}
	for _, workID := range works {
		if err := d.Physical.RecomputeWorkItems(ctx, workID); err != nil {
			outcome.Problems = append(outcome.Problems, fmt.Sprintf("work %d: counts will catch up at the next scan: %v", workID, err))
		}
	}
	outcome.Linked++
	return size, nil
}

// sourceFor picks the path of the kept copy a target is linked to: the same extension, on the same root first.
func sourceFor(keep []duplicatePath, target duplicatePath) (duplicatePath, bool) {
	extension := strings.ToLower(path.Ext(target.rel))
	var other *duplicatePath
	for i := range keep {
		if strings.ToLower(path.Ext(keep[i].rel)) != extension {
			continue
		}
		if keep[i].rootID == target.rootID {
			return keep[i], true
		}
		if other == nil {
			other = &keep[i]
		}
	}
	if other == nil {
		return duplicatePath{}, false
	}
	return *other, true
}
