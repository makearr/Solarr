package adult

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// GuessFloor is the least confidence a held folder's guessed scene is placed at by "Commit anyway".
const GuessFloor = 0.75

// defaultSceneMinutes is what a scene is assumed to run when nothing says, for the sample check.
const defaultSceneMinutes = 20

// ImporterConfig is what an Importer needs; Logger, RenameOnImport, ExtraFiles, Probe, AfterPlacing and Exclusions are
// optional.
type ImporterConfig struct {
	works.ImportDeps
	Resolver Resolver
	// Probe measures a downloaded file for the sample and audio checks; nil skips those two checks.
	Probe func(ctx context.Context, absolutePath string) (measured mediatool.Measured, ok bool)
}

// Importer is the adult module's batch importer: it works out which scene a download holds, names and places its video
// file below the studio's folder, and records it as the scene's. What it cannot place waits in the Review queue.
type Importer struct {
	works.Importer
}

// placing is the scene's part of works.Importer.
type placing struct {
	config ImporterConfig
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	return Importer{works.NewImporter(works.ImporterConfig{
		MediaType: MediaType, Noun: "scene", GuessFloor: GuessFloor, Units: placing{config: config},
		Acquisitions: config.Acquisitions, Desired: config.Desired, Logger: config.Logger, AfterPlacing: config.AfterPlacing,
	})}
}

// MediaSizeFloorBytes implements batch.SizeFloor.
func (Importer) MediaSizeFloorBytes() int64 { return batch.VideoSizeFloorBytes }

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
	_ batch.SizeFloor     = Importer{}
)

// Resolve implements works.UnitPlacer: the scene from its file's fingerprint and name, else from the download's name.
func (p placing) Resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	file := works.LargestFile(unit.Media)
	if file == nil {
		return 0, nil
	}
	oshash, _ := library.OSHash(p.config.FS, path.Join(request.SourcePath, file.RelPath), file.SizeBytes)
	var candidates []model.Identification
	info, parsed := parseadult.ParsePath(file.RelPath)
	if parsed || oshash != "" {
		workID, found := p.config.Resolver.Resolve(ctx, request.CategoryID, info, oshash)
		if workID != 0 {
			return workID, nil
		}
		candidates = found
	}
	if release, ok := parseadult.ParseName(path.Base(path.Join(request.SourcePath, unit.Directory))); ok {
		if workID, found := p.config.Resolver.Resolve(ctx, request.CategoryID, release, ""); workID != 0 || len(candidates) == 0 {
			return workID, found
		}
	}
	return 0, candidates
}

// ResolveNamed implements works.UnitPlacer: a film's id adds the film, a scene's finds the scene.
func (p placing) ResolveNamed(ctx context.Context, categoryID int64, named batch.Named) (int64, error) {
	if named.Provider == ProviderFilm {
		return p.config.Resolver.AddFilm(ctx, categoryID, named.ExternalID)
	}
	ids, ok := NamedIDs(named.Provider, named.ExternalID)
	if !ok {
		return 0, fmt.Errorf("%w: %s %q is not a scene id", media.ErrInvalidAdd, named.Provider, named.ExternalID)
	}
	if p.config.Exclusions != nil {
		if err := p.config.Exclusions.Allow(ctx, MediaType, named.Provider, named.ExternalID); err != nil {
			return 0, err
		}
	}
	return p.config.Resolver.ByIDs(ctx, categoryID, ids, Hint{}), nil
}

// Place implements works.UnitPlacer.
func (p placing) Place(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	return works.PlaceSingleFile(ctx, p.config.ImportDeps, works.SingleFile{
		MediaType: MediaType, Noun: "scene", DefaultMinutes: defaultSceneMinutes, Probe: p.config.Probe, OSHash: true,
		AffinityKey: func(basics library.WorkBasics) string { return affinityKey(basics.Title) },
		Name:        p.name,
	}, request, unit, workID, upgrade)
}

// name renders where a scene's file goes below its root.
func (p placing) name(ctx context.Context, request batch.Request, workID int64, _ library.WorkBasics, file *inspect.File, now time.Time) (string, settings.FileManagement, error) {
	names, fileManagement := p.namingFor(request.CategoryID)
	facts, err := NamingFacts(ctx, p.config.Reader, request.CategoryID, workID, now)
	if err != nil {
		return "", fileManagement, err
	}
	facts.OriginalFilename = strings.TrimSuffix(path.Base(file.RelPath), path.Ext(file.RelPath))
	if info, ok := parseadult.ParseName(facts.OriginalFilename); ok {
		facts.ReleaseGroup = info.Group
	}
	destRel, _, err := names.RenderScene(facts, file.RelPath)
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

// PlacementKey is the affinity key a scene places under, for a root chosen before its first file.
func PlacementKey(title string) string { return affinityKey(title) }

// affinityKey keeps a scene's files in one folder on one root.
func affinityKey(title string) string {
	clean := strings.ToLower(strings.TrimSpace(title))
	if clean == "" {
		return ""
	}
	return MediaType + ":" + clean
}

// NamingFacts gathers what a scene's file names draw on: its title, studio, day, performers and ids.
func NamingFacts(ctx context.Context, reader library.SeriesReader, categoryID, workID int64, now time.Time) (FileNaming, error) {
	detail, err := reader.GetOf(ctx, MediaType, categoryID, workID, now)
	if err != nil {
		return FileNaming{}, fmt.Errorf("scene %d: %w", workID, err)
	}
	facts := FileNaming{
		Title: detail.Summary.Title, Year: int(detail.Summary.Year), Studio: detail.Summary.Fields[FieldStudio], Network: detail.Summary.Fields[FieldNetwork],
		Date: detail.Summary.Fields[FieldRelease], Code: detail.Summary.Fields[FieldCode], StashID: detail.IDs[ProviderStashDB],
	}
	for _, credit := range detail.Credits {
		if credit.Role == RolePerformer {
			facts.Performers = append(facts.Performers, credit.Name)
		}
	}
	return facts, nil
}
