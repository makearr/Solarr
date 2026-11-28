package movie

import (
	"context"
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
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
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
	info, ok := parsemovie.ParsePathAt(file.RelPath, p.config.Clock.Now().Year())
	if !ok {
		return 0, nil
	}
	return p.config.Resolver.Resolve(ctx, request.CategoryID, info, NFOIDs(p.config.FS, path.Join(request.SourcePath, file.RelPath)))
}

// ResolveByName implements works.UnitPlacer: Resolve without the nfo beside the file.
func (p placing) ResolveByName(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	file := works.LargestFile(unit.Media)
	if file == nil {
		return 0, nil
	}
	info, ok := parsemovie.ParsePathAt(file.RelPath, p.config.Clock.Now().Year())
	if !ok {
		return 0, nil
	}
	return p.config.Resolver.Resolve(ctx, request.CategoryID, info, IDs{})
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
	return works.PlaceSingleFile(ctx, p.config.ImportDeps, works.SingleFile{
		MediaType: MediaType, Noun: "movie", DefaultMinutes: defaultMovieMinutes, Probe: p.config.Probe,
		AffinityKey: func(basics library.WorkBasics) string { return affinityKey(basics.Title, basics.Year) },
		Name:        p.name,
	}, request, unit, workID, upgrade)
}

// name renders where a film's file goes below its root.
func (p placing) name(ctx context.Context, request batch.Request, workID int64, basics library.WorkBasics, file *inspect.File, now time.Time) (string, settings.FileManagement, error) {
	names, fileManagement := p.namingFor(request.CategoryID)
	ids, err := p.config.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return "", fileManagement, err
	}
	info, _ := parsemovie.ParsePathAt(file.RelPath, now.Year())
	destRel, _, err := names.RenderMovie(fileNaming(basics, ids, info, file), file.RelPath)
	if err != nil {
		return "", fileManagement, fmt.Errorf("name %q: %w", file.RelPath, err)
	}
	return destRel, fileManagement, nil
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

// ReleaseFactsOf reads a release name for the facts an import keeps with the files it brought (QUAL-1); false when it says none.
func (Importer) ReleaseFactsOf(title string) (batch.ReleaseFacts, bool) {
	info, found := parsemovie.Parse(title)
	if !found || (!info.Claims.HasQuality() && info.ReleaseGroup == "") {
		return batch.ReleaseFacts{}, false
	}
	return batch.ReleaseFacts{Source: releasefacts.Source(info.Claims), Group: info.ReleaseGroup, Revision: max(info.Claims.Version, 1)}, true
}
