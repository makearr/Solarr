package movie

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// defaultMovieMinutes is what Radarr assumes a film runs when nothing says.
const defaultMovieMinutes = 90

// ImporterConfig is what an Importer needs; Logger, RenameOnImport, ExtraFiles, Probe, AfterPlacing and Exclusions are
// optional.
type ImporterConfig struct {
	works.ImportDeps
	Resolver Resolver
	// Probe measures a downloaded file for the sample and audio checks; nil skips those two checks.
	Probe func(ctx context.Context, absolutePath string) (measured mediatool.Measured, ok bool)
}

// Importer is the movie module's batch importer: it works out which film a download folder holds, names and places its
// video file below the movie folder, and records it as a file of the film. What it cannot place waits in the Review queue.
type Importer struct {
	works.Importer
}

// placing is the movie's part of works.Importer.
type placing struct {
	config ImporterConfig
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	return Importer{works.NewImporter(works.ImporterConfig{
		MediaType: MediaType, Noun: "film", GuessFloor: GuessFloor, Units: placing{config: config},
		Acquisitions: config.Acquisitions, Desired: config.Desired, Logger: config.Logger, AfterPlacing: config.AfterPlacing,
	})}
}

// MediaSizeFloorBytes implements batch.SizeFloor: a sample or a stub is no film to import.
func (Importer) MediaSizeFloorBytes() int64 { return batch.VideoSizeFloorBytes }

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
	_ batch.SizeFloor     = Importer{}
)

// Resolve implements works.UnitPlacer: the film from its folder and file names, and the ids an nfo beside the file names.
func (p placing) Resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	file := works.LargestFile(unit.Media)
	if file == nil {
		return 0, nil
	}
	info, ok := parsemovie.ParsePath(file.RelPath)
	if !ok {
		return 0, nil
	}
	return p.config.Resolver.Resolve(ctx, request.CategoryID, info, NFOIDs(p.config.FS, path.Join(request.SourcePath, file.RelPath)))
}

// ResolveNamed implements works.UnitPlacer: a TMDB or IMDb id a person named.
func (p placing) ResolveNamed(ctx context.Context, categoryID int64, named batch.Named) (int64, error) {
	ids, err := namedIDs(named.Provider, named.ExternalID)
	if err != nil {
		return 0, err
	}
	if p.config.Exclusions != nil {
		if err := p.config.Exclusions.Allow(ctx, MediaType, named.Provider, named.ExternalID); err != nil {
			return 0, err
		}
	}
	return p.config.Resolver.byID(ctx, categoryID, ids), nil
}

// ErrNotAMovieID is a named film whose id is not a TMDB or IMDb one.
var ErrNotAMovieID = fmt.Errorf("%w: that is not a movie id", media.ErrInvalidAdd)

func namedIDs(provider, externalID string) (IDs, error) {
	switch provider {
	case ProviderTMDB:
		if id, err := strconv.ParseInt(externalID, 10, 64); err == nil && id > 0 {
			return IDs{TMDB: id}, nil
		}
	case ProviderIMDb:
		if strings.HasPrefix(externalID, "tt") {
			return IDs{IMDb: externalID}, nil
		}
	}
	return IDs{}, fmt.Errorf("%w: %s %q", ErrNotAMovieID, provider, externalID)
}

// Place implements works.UnitPlacer.
func (p placing) Place(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := p.config.Clock.Now()
	file := works.LargestFile(unit.Media)
	if file == nil {
		return nil, batch.ErrNothingToPlace
	}
	// A second file is only for a film that wants a better one (Radarr's "existing file is better" rule).
	roots, err := p.config.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("movie %d: find existing roots: %w", workID, err)
	}
	if len(roots) > 0 && !upgrade {
		return nil, batch.ErrNothingToPlace
	}
	basics, err := p.config.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("movie %d: %w", workID, err)
	}
	sourceAbs := path.Join(request.SourcePath, file.RelPath)
	siblings := make([]string, 0, len(unit.Media))
	for _, other := range unit.Media {
		siblings = append(siblings, path.Join(request.SourcePath, other.RelPath))
	}
	if rejection := works.ScreenVideo(ctx, p.config.Probe, sourceAbs, siblings, int(basics.RuntimeMs/60_000), defaultMovieMinutes); rejection != nil {
		return nil, &batch.RejectedError{MediaType: MediaType, Rejections: []model.Rejection{*rejection}}
	}
	info, _ := parsemovie.ParsePath(file.RelPath)

	hashes, err := p.config.Placer.CheckBlocked(ctx, request.SourcePath, []placer.File{{RelPath: file.RelPath, SizeBytes: file.SizeBytes}})
	if err != nil {
		return nil, err
	}
	names, fileManagement := p.namingFor(request.CategoryID)
	destRoot, err := p.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(basics.Title, basics.Year), file.SizeBytes)
	if err != nil {
		return nil, err
	}
	ids, err := p.config.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return nil, err
	}
	destRel, _, err := names.RenderMovie(fileNaming(basics, ids, info, file), file.RelPath)
	if err != nil {
		return nil, fmt.Errorf("name %q: %w", file.RelPath, err)
	}
	if upgrade {
		destRel = p.config.Placer.FreeName(destRoot.Path, destRel)
	}
	destAbs := path.Join(destRoot.Path, destRel)
	methods := p.config.Placer.Methods(request.Ladder)
	method, err := mover.PlaceOne(p.config.FS, sourceAbs, destAbs, file.SizeBytes, methods)
	if err != nil {
		return nil, fmt.Errorf("place %q: %w", file.RelPath, err)
	}
	placed := []placer.Placed{{Source: sourceAbs, Dest: destAbs, Rel: destRel, Hash: hashes[0], Size: file.SizeBytes, Method: method}}
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, p.config.Placer.Undo(placed))
	}
	if method != mover.PlaceSymlink {
		p.config.Placer.Finish(destRoot, destAbs, fileManagement, time.Time{})
	}

	registered, err := p.config.Physical.RegisterBatch(ctx, destRoot.ID, []library.PhysicalFile{{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[0], FirstSeenAt: now}})
	if err != nil {
		return undo(fmt.Errorf("register placed file: %w", err))
	}
	if _, err := p.config.Series.AttachWholeFiles(ctx, now, workID, destRoot.ID, []int64{registered[0].MediaObjectID}); err != nil {
		return undo(err)
	}
	placement := &library.Placement{
		WorkID: workID, MediaObjectIDs: []int64{registered[0].MediaObjectID}, LibraryPathIDs: []int64{registered[0].LibraryPathID},
		TransferMethods: []string{string(method)},
	}
	if p.config.ExtraFiles != nil {
		owners := []placer.Owner{{SourceRel: file.RelPath, DestRel: destRel}}
		placement.Extras = p.config.Placer.Extras(p.config.ExtraFiles(request.CategoryID), request.SourcePath, destRoot, unit.Other, owners, methods)
	}
	return placement, nil
}

func (p placing) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	profile, fileManagement, mayRename := works.CategoryNaming(p.config.Categories, categoryID, p.config.RenameOnImport)
	names := NamingFor(profile)
	names.RenameFiles = names.RenameFiles && mayRename
	return names, fileManagement
}

// PlacementKey is the affinity key a film places under, for a root chosen before its first file.
func PlacementKey(title string, year int64) string { return affinityKey(title, year) }

// affinityKey keeps a film's versions in one folder on one root; two films of a title in different years stay apart.
func affinityKey(title string, year int64) string {
	clean := strings.ToLower(strings.TrimSpace(title))
	if clean == "" {
		return ""
	}
	return "movie:" + clean + ":" + strconv.FormatInt(year, 10)
}

// fileNaming gathers everything a placed file's name can draw on.
func fileNaming(basics library.WorkBasics, ids map[string]string, info parsemovie.Info, file *inspect.File) FileNaming {
	naming := FileNaming{
		Title: basics.Title, Year: int(basics.Year), Edition: info.Edition,
		QualityTitle: releasefacts.QualityTitle(info.Claims), Proper: info.Claims.Version > 1, Version: info.Claims.Version,
		VideoCodec: strings.ToUpper(info.Claims.VideoCodec), DynamicRange: info.Claims.HDR,
		AudioCodec: strings.ToUpper(info.Claims.AudioCodec), AudioChannels: info.Claims.AudioChannels,
		ReleaseGroup: info.ReleaseGroup, OriginalTitleRel: info.ReleaseTitle,
		OriginalFilename: strings.TrimSuffix(path.Base(file.RelPath), path.Ext(file.RelPath)),
	}
	naming.IDs.TMDB, _ = strconv.ParseInt(ids[ProviderTMDB], 10, 64)
	naming.IDs.IMDb = ids[ProviderIMDb]
	return naming
}
