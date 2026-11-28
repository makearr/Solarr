package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// VideoCommitter places the video files of a download as unverified video works, the way a scan records them.
type VideoCommitter interface {
	CommitVideoUnit(ctx context.Context, sourceRoot string, unit inspect.Unit, probes []mediatool.Probe, identities []*VideoIdentity, categoryID int64, ladder []string) (Placed, error)
}

var _ VideoCommitter = Committer{}

// CommitVideoUnit places each video of the unit under its artist's folder, named "Artist - Title (year)", and records
// it as one unverified `local:video` work credited to that artist, as the scan does for a video already in the library.
// probes and identities (nil: not named by MusicBrainz) parallel unit.Media. The result describes the whole unit; its work is the first video's.
func (c Committer) CommitVideoUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, probes []mediatool.Probe, identities []*VideoIdentity, categoryID int64, ladder []string,
) (Placed, error) {
	if len(unit.Media) == 0 || len(probes) != len(unit.Media) || len(identities) != len(unit.Media) {
		return Placed{}, fmt.Errorf("music: commit video unit %q: %d files, %d probes", unit.Directory, len(unit.Media), len(probes))
	}
	quickHashes, err := c.quickHashesChecked(ctx, sourceRoot, unit)
	if err != nil {
		return Placed{}, err
	}
	kind, names, fileManagement := "", NamingFor(nil), settings.DefaultFileManagement
	if category, found := c.categories.Category(categoryID); found {
		kind, names, fileManagement = category.Kind, NamingFor(category.Naming), category.FileManagement
	}
	claims := make([]VideoClaim, len(unit.Media))
	for index, media := range unit.Media {
		claims[index] = InspectVideo(media.RelPath, probes[index], kind)
		if claims[index].Artist == "" {
			claims[index].Artist, _ = folderNames(sourceRoot, unit.Directory)
		}
	}
	destRoot, err := c.resolveDestRoot(ctx, categoryID, claims[0].Artist, unitBytes(unit), unit.Directory)
	if err != nil {
		return Placed{}, err
	}
	methods := c.transferMethods(ladder)
	now := c.clock.Now()

	var placedSoFar []placedFile
	var registered []library.RegisteredFile
	undo := func(err error) (Placed, error) {
		return Placed{}, errors.Join(err, c.undoPlaced(placedSoFar), c.physical.Unregister(context.WithoutCancel(ctx), registered))
	}
	destRelPaths := make([]string, len(unit.Media))
	files := make([]library.PhysicalFile, len(unit.Media))
	transferMethods := make([]mover.PlaceMethod, len(unit.Media))
	for index, media := range unit.Media {
		destRelPath, err := videoRelPath(names, claims[index], media.RelPath)
		if err != nil {
			return undo(fmt.Errorf("music: commit video unit %q: %w", unit.Directory, err))
		}
		sourceAbs, destAbs := path.Join(sourceRoot, media.RelPath), path.Join(destRoot.Path, destRelPath)
		method, err := mover.PlaceOneOnDisk(ctx, c.fsys, sourceAbs, destAbs, media.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("music: place %q: %w", media.RelPath, err))
		}
		placedSoFar = append(placedSoFar, placedFile{source: sourceAbs, dest: destAbs, sizeBytes: media.SizeBytes, method: method})
		if method != mover.PlaceSymlink {
			_ = c.finishFile(destRoot.Path, destAbs, fileManagement, probes[index].Tags)
		}
		transferMethods[index], destRelPaths[index] = method, destRelPath
		files[index] = library.PhysicalFile{RelPath: destRelPath, SizeBytes: media.SizeBytes, QuickHash: quickHashes[index], FirstSeenAt: now}
	}
	registered, err = c.physical.RegisterBatch(ctx, destRoot.ID, files)
	if err != nil {
		return undo(fmt.Errorf("music: register placed files for unit %q: %w", unit.Directory, err))
	}
	links := make([]library.WorkLink, len(registered))
	mediaObjectIDs := make([]int64, len(registered))
	libraryPathIDs := make([]int64, len(registered))
	for index, file := range registered {
		mediaObjectIDs[index], libraryPathIDs[index] = file.MediaObjectID, file.LibraryPathID
		links[index] = videoLink(claims[index], identities[index], localVideoKey(destRoot.Path, destRelPaths[index]), destRoot.ID, file.MediaObjectID)
	}
	resolutions, err := c.identity.ResolveWorksDetailed(ctx, now, links)
	if err != nil {
		return undo(fmt.Errorf("music: resolve identity for video unit %q: %w", unit.Directory, err))
	}
	for index, resolution := range resolutions {
		if err := linkVideoAlbum(ctx, c.identity, now, resolution.WorkID, identities[index]); err != nil {
			return Placed{}, fmt.Errorf("music: video unit %q: %w", unit.Directory, err)
		}
		if category, found := c.categories.Category(categoryID); found && category.MusicVideos && identities[index] != nil {
			if err := attachVideoFile(ctx, c.desired, categoryID, resolution.WorkID, mediaObjectIDs[index], now.Unix()); err != nil {
				return Placed{}, fmt.Errorf("music: video unit %q: %w", unit.Directory, err)
			}
		}
		if (!resolution.WorkCreated && identities[index] != nil) || claims[index].Artist == "" || c.people == nil {
			continue
		}
		personID, err := c.people.ResolveFolderArtist(ctx, now, ProviderLocalArtist, claims[index].Artist)
		if err != nil {
			return Placed{}, fmt.Errorf("music: video unit %q: artist: %w", unit.Directory, err)
		}
		if err := c.people.AddLeadCredit(ctx, resolution.WorkID, personID, CreditRoleArtist); err != nil {
			return Placed{}, fmt.Errorf("music: video unit %q: credit: %w", unit.Directory, err)
		}
	}
	first := resolutions[0]
	return Placed{
		WorkID: first.WorkID, WorkCreated: first.WorkCreated, EditionID: first.EditionID, EditionCreated: first.EditionCreated,
		MediaObjectIDs: mediaObjectIDs, LibraryPathIDs: libraryPathIDs, TransferMethods: transferMethods,
		RootFolderID: destRoot.ID,
	}, nil
}

// videoRelPath is "Artist/Artist - Title (year).ext" under the library's artist folder format; the name is what a
// later scan reads the claim back from.
func videoRelPath(names Naming, claim VideoClaim, sourceRelPath string) (string, error) {
	folder, err := names.RenderArtistFolder(claim.Artist)
	if err != nil {
		return "", fmt.Errorf("render artist folder: %w", err)
	}
	name := claim.Title
	if claim.Artist != "" {
		name = claim.Artist + " - " + claim.Title
	}
	if claim.Year != 0 {
		name += fmt.Sprintf(" (%d)", claim.Year)
	}
	name = fsx.SanitiseComponent(strings.TrimSpace(name))
	if name == "" {
		name = fsx.SanitiseComponent(strings.TrimSuffix(path.Base(sourceRelPath), path.Ext(sourceRelPath)))
	}
	return path.Join(folder, name+strings.ToLower(path.Ext(sourceRelPath))), nil
}
