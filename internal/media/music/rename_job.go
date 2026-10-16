package music

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// RenameRoots resolves one root folder's own policy and filesystem facts.
// Only the type is taken from internal/db/settings (the same lightweight use
// internal/scanning/scan's own Roots interface already makes) -- actual cache access
// stays behind this interface, satisfied by the same adapter
// cmd/solarr already built for internal/moving/drainjob.Roots, a strict
// superset of this one method.
type RenameRoots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// RenameFiles is the library-side fact a preview needs: every real file on
// the root. Satisfied directly by library.Physical.
type RenameFiles interface {
	PathsForRoot(ctx context.Context, rootFolderID int64) ([]library.FileOnRoot, error)
	PathsForWorkOnRoot(ctx context.Context, rootFolderID, workID int64) ([]library.FileOnRoot, error)
}

// RenameIdentities resolves a file's editorial identity. Satisfied directly
// by library.Desired.
type RenameIdentities interface {
	IdentityForFile(ctx context.Context, mediaObjectID int64) (library.FileIdentity, bool, error)
}

// RenameWorkDetails answers a work's full detail. Satisfied directly by
// library.Works.
type RenameWorkDetails interface {
	Detail(ctx context.Context, workID int64) (library.WorkDetail, error)
}

// RenameMoveJobs is the library-side persistence a commit needs. Satisfied
// directly by library.MoveJobs.
type RenameMoveJobs interface {
	Create(ctx context.Context, params library.NewMoveJob) (int64, error)
	AddItems(ctx context.Context, jobID int64, items []library.NewMoveItem) error
}

// RenameConfig is what RenamePreview and RenameStart need. Every field is
// required except Clock, which is optional and defaults to the real clock.
type RenameConfig struct {
	// Categories answers each library's naming; a work in none uses the scheme's default.
	Categories Categories
	Roots      RenameRoots
	Files      RenameFiles
	Identities RenameIdentities
	Works      RenameWorkDetails
	MoveJobs   RenameMoveJobs
	Queue      *jobs.Queue
	Clock      clock.Clock
}

// RenamePreview computes every changed rename target on one root, under the
// shipped default template -- section 11.8's own "preview as a diff"
// (Invariant 73). A file with no editorial identity (never aligned to a
// desired_item) is skipped, and so is one whose identity names no real
// work_part in its own work -- neither is fatal to the whole preview, the
// same "one bad file does not fail the batch" reasoning ADR-48/ADR-51
// already apply elsewhere. A work's own WorkDetail is fetched at most once
// per call, since every track on an album shares it.
func RenamePreview(
	ctx context.Context, cfg RenameConfig, rootFolderID int64, scope media.RenameScope,
) ([]RenameTarget, error) {
	var files []library.FileOnRoot
	var err error
	if scope.WorkID != 0 {
		files, err = cfg.Files.PathsForWorkOnRoot(ctx, rootFolderID, scope.WorkID)
	} else {
		files, err = cfg.Files.PathsForRoot(ctx, rootFolderID)
	}
	if err != nil {
		return nil, fmt.Errorf("music: list files on root %d: %w", rootFolderID, err)
	}
	var onlyCategoryID *int64
	if scope.CategoryID != 0 {
		onlyCategoryID = &scope.CategoryID
	}

	details := map[int64]library.WorkDetail{}
	var targets []RenameTarget
	for _, f := range files {
		identity, found, err := cfg.Identities.IdentityForFile(ctx, f.MediaObjectID)
		if err != nil {
			return nil, fmt.Errorf("music: identity for media object %d: %w", f.MediaObjectID, err)
		}
		if !found || (scope.WorkID != 0 && identity.WorkID != scope.WorkID) {
			continue
		}
		detail, cached := details[identity.WorkID]
		if !cached {
			detail, err = cfg.Works.Detail(ctx, identity.WorkID)
			if err != nil {
				return nil, fmt.Errorf("music: work detail for %d: %w", identity.WorkID, err)
			}
			details[identity.WorkID] = detail
		}
		names, inScope := cfg.namingFor(detail.CategoryIDs, onlyCategoryID)
		if !inScope {
			continue
		}
		target, err := RenameTargetFor(names, detail, identity, f.MediaObjectID, f.SizeBytes, f.RelPath)
		if err != nil {
			continue
		}
		if target.Changed() {
			targets = append(targets, target)
		}
	}
	return targets, nil
}

// RenameStart computes the same preview RenamePreview would, then plans it
// (one affinity group per work, so an album renames as one unit under
// mover.Plan's own guards -- read-only+allow_rename, and the same-root
// reserve exemption, both apply here exactly as they do for any other
// RENAME request). With preview true, or when the plan refuses outright or
// there is nothing to rename, nothing is persisted and moveJobID is 0 --
// the caller reads plan.Refused for why. Otherwise the move job is
// persisted and moverjob's own Apply is enqueued. warnings is keyed by
// MediaObjectID, RenameTargetFor's own naming.RenderPath warnings carried
// through -- mover.Plan is generic across every move reason and does not
// know what a naming warning is (ADR-23), so this stays a separate return
// rather than a new field on mover.Move.
func RenameStart(
	ctx context.Context, cfg RenameConfig, rootFolderID int64, scope media.RenameScope, preview bool,
) (mover.MovePlan, int64, map[int64][]string, error) {
	targets, err := RenamePreview(ctx, cfg, rootFolderID, scope)
	if err != nil {
		return mover.MovePlan{}, 0, nil, err
	}
	moves := make([]renamejob.Target, len(targets))
	for i, target := range targets {
		moves[i] = renamejob.Target(target)
	}
	return renamejob.Start(ctx, renamejob.Config{Roots: cfg.Roots, MoveJobs: cfg.MoveJobs, Queue: cfg.Queue, Clock: cfg.Clock},
		rootFolderID, scope.CategoryID, moves, preview)
}

// namingFor is the naming of the work's library -- onlyCategoryID when given, which also
// scopes the rename to that library's works; otherwise the first library that has one.
func (cfg RenameConfig) namingFor(categoryIDs []int64, onlyCategoryID *int64) (Naming, bool) {
	if onlyCategoryID != nil {
		if !slices.Contains(categoryIDs, *onlyCategoryID) {
			return Naming{}, false
		}
		categoryIDs = []int64{*onlyCategoryID}
	}
	if cfg.Categories != nil {
		for _, id := range categoryIDs {
			if category, found := cfg.Categories.Category(id); found {
				return NamingFor(category.Naming), true
			}
		}
	}
	return NamingFor(nil), true
}
