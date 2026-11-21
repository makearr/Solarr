package works

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// SingleFile is what differs between the media types that place the largest video of a unit as one work's file.
type SingleFile struct {
	MediaType string
	// Noun is how the work is named in an error ("movie", "scene").
	Noun string
	// DefaultMinutes is the runtime the sample check assumes when the provider gave none.
	DefaultMinutes int
	// Probe measures the file for the sample and audio checks; nil skips those two.
	Probe func(ctx context.Context, absolutePath string) (measured mediatool.Measured, ok bool)
	// AffinityKey groups the works that should share a root folder.
	AffinityKey func(basics library.WorkBasics) string
	// Name answers the destination path below the root and the file management of the library.
	Name func(ctx context.Context, request batch.Request, workID int64, basics library.WorkBasics, file *inspect.File, now time.Time) (destRel string, management settings.FileManagement, err error)
	// OSHash stores the placed file's OpenSubtitles hash.
	OSHash bool
}

// PlaceSingleFile places the largest video of a unit as the file of workID, and takes it back whole when a later step fails.
func PlaceSingleFile(
	ctx context.Context, deps ImportDeps, kind SingleFile, request batch.Request, unit inspect.Unit, workID int64, upgrade bool,
) (*library.Placement, error) {
	now := deps.Clock.Now()
	file := LargestFile(unit.Media)
	if file == nil {
		return nil, batch.ErrNothingToPlace
	}
	// A second file is only for a work that wants a better one.
	roots, err := deps.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("%s %d: find existing roots: %w", kind.Noun, workID, err)
	}
	if len(roots) > 0 && !upgrade {
		return nil, batch.ErrNothingToPlace
	}
	basics, err := deps.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("%s %d: %w", kind.Noun, workID, err)
	}
	sourceAbs := path.Join(request.SourcePath, file.RelPath)
	siblings := make([]string, 0, len(unit.Media))
	for _, other := range unit.Media {
		siblings = append(siblings, path.Join(request.SourcePath, other.RelPath))
	}
	if rejection := ScreenVideo(ctx, kind.Probe, sourceAbs, siblings, int(basics.RuntimeMs/60_000), kind.DefaultMinutes); rejection != nil {
		return nil, &batch.RejectedError{MediaType: kind.MediaType, Rejections: []model.Rejection{*rejection}}
	}
	hashes, err := deps.Placer.CheckBlocked(ctx, request.SourcePath, []placer.File{{RelPath: file.RelPath, SizeBytes: file.SizeBytes}})
	if err != nil {
		return nil, err
	}
	destRoot, err := deps.Placer.ChooseRoot(ctx, request.CategoryID, workID, kind.AffinityKey(basics), file.SizeBytes)
	if err != nil {
		return nil, err
	}
	destRel, management, err := kind.Name(ctx, request, workID, basics, file, now)
	if err != nil {
		return nil, err
	}
	if upgrade {
		destRel = deps.Placer.FreeName(destRoot.Path, destRel)
	}
	destAbs := path.Join(destRoot.Path, destRel)
	methods := deps.Placer.Methods(request.Ladder)
	method, err := mover.PlaceOneOnDisk(ctx, deps.FS, sourceAbs, destAbs, file.SizeBytes, methods)
	if err != nil {
		return nil, fmt.Errorf("place %q: %w", file.RelPath, err)
	}
	placed := []placer.Placed{{Source: sourceAbs, Dest: destAbs, Rel: destRel, Hash: hashes[0], Size: file.SizeBytes, Method: method}}
	var registered []library.RegisteredFile
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, deps.Placer.Undo(placed), deps.Physical.Unregister(context.WithoutCancel(ctx), registered))
	}
	if method != mover.PlaceSymlink {
		deps.Placer.Finish(destRoot, destAbs, management, time.Time{})
	}
	physical := library.PhysicalFile{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[0], FirstSeenAt: now}
	if kind.OSHash {
		physical.OSHash, _ = library.OSHash(deps.FS, destAbs, file.SizeBytes)
	}
	registered, err = deps.Physical.RegisterBatch(ctx, destRoot.ID, []library.PhysicalFile{physical})
	if err != nil {
		return undo(fmt.Errorf("register placed file: %w", err))
	}
	if _, err := deps.Series.AttachWholeFiles(ctx, now, workID, destRoot.ID, []int64{registered[0].MediaObjectID}); err != nil {
		return undo(err)
	}
	placement := &library.Placement{
		WorkID: workID, MediaObjectIDs: []int64{registered[0].MediaObjectID}, LibraryPathIDs: []int64{registered[0].LibraryPathID},
		TransferMethods: []string{string(method)},
	}
	if deps.ExtraFiles != nil {
		owners := []placer.Owner{{SourceRel: file.RelPath, DestRel: destRel}}
		placement.Extras = deps.Placer.Extras(ctx, deps.ExtraFiles(request.CategoryID), request.SourcePath, destRoot, unit.Other, owners, methods)
	}
	return placement, nil
}
