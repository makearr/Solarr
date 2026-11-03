package adult

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/importing/rules"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// GuessFloor is the least confidence a held folder's guessed scene is placed at by "Commit anyway".
const GuessFloor = 0.75

// defaultSceneMinutes is what a scene is assumed to run when nothing says, for the sample check.
const defaultSceneMinutes = 20

// ImporterConfig is what an Importer needs; Logger, RenameOnImport, ExtraFiles, Probe, AfterPlacing and Exclusions are
// optional.
type ImporterConfig struct {
	FS           fsx.FS
	Placer       placer.Placer
	Physical     library.Physical
	Series       library.Series
	Reader       library.SeriesReader
	Identity     library.Identity
	Desired      library.Desired
	Acquisitions batch.AcquisitionSource
	Resolver     Resolver
	Categories   interface {
		Category(id int64) (settings.Category, bool)
	}
	// Exclusions lifts a scene's exclusion when a person names it for a held unit.
	Exclusions interface {
		Allow(ctx context.Context, mediaType, provider, externalID string) error
	}
	Clock          clock.Clock
	Logger         *slog.Logger
	RenameOnImport func() bool
	// ExtraFiles lists the extensions of files brought along with the scene (its poster, an nfo); nil brings none.
	ExtraFiles func(categoryID int64) []string
	// Probe measures a downloaded file for the sample and audio checks; nil skips those two checks.
	Probe func(ctx context.Context, absolutePath string) (measured mediatool.Measured, ok bool)
	// AfterPlacing is told of each scene an import placed a file of.
	AfterPlacing func(ctx context.Context, categoryID, workID int64)
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
	file := largest(unit.Media)
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

// largest is the unit's biggest video file, which is the scene; the rest are samples.
func largest(files []inspect.File) *inspect.File {
	var best *inspect.File
	for index := range files {
		if best == nil || files[index].SizeBytes > best.SizeBytes {
			best = &files[index]
		}
	}
	return best
}

// screen holds the scene's file against the checks made before an import: still being unpacked, one part of several, a
// sample by its runtime, no audio track. nil lets it through.
func (p placing) screen(ctx context.Context, sourceAbs string, siblings []string, expectedMinutes int) *model.Rejection {
	for _, rejection := range []*model.Rejection{rules.Unpacking(sourceAbs, false), rules.MultiPart(sourceAbs, siblings)} {
		if rejection != nil {
			return rejection
		}
	}
	if p.config.Probe == nil {
		return nil
	}
	measured, ok := p.config.Probe(ctx, sourceAbs)
	if expectedMinutes == 0 {
		expectedMinutes = defaultSceneMinutes
	}
	if verdict := rules.Sample(rules.SampleFacts{Path: sourceAbs, Runtime: measured.Duration, Probed: ok, ExpectedMinutes: expectedMinutes}); verdict == rules.VerdictSample {
		return rules.RejectSample(verdict, false)
	}
	audioStreams := 0
	if measured.AudioCodec != "" {
		audioStreams = 1
	}
	return rules.HasAudio(ok && measured.VideoCodec != "", audioStreams)
}

// placeUnit places the scene's file. A scene that already has a file takes another only as an upgrade.
// Place implements works.UnitPlacer.
func (p placing) Place(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := p.config.Clock.Now()
	file := largest(unit.Media)
	if file == nil {
		return nil, batch.ErrNothingToPlace
	}
	roots, err := p.config.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("scene %d: find existing roots: %w", workID, err)
	}
	if len(roots) > 0 && !upgrade {
		return nil, batch.ErrNothingToPlace
	}
	basics, err := p.config.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("scene %d: %w", workID, err)
	}
	sourceAbs := path.Join(request.SourcePath, file.RelPath)
	siblings := make([]string, 0, len(unit.Media))
	for _, other := range unit.Media {
		siblings = append(siblings, path.Join(request.SourcePath, other.RelPath))
	}
	// A scene is as long as its provider says; the sample check scales to it.
	if rejection := p.screen(ctx, sourceAbs, siblings, int(basics.RuntimeMs/60_000)); rejection != nil {
		return nil, &batch.RejectedError{MediaType: MediaType, Rejections: []model.Rejection{*rejection}}
	}
	hashes, err := p.config.Placer.CheckBlocked(ctx, request.SourcePath, []placer.File{{RelPath: file.RelPath, SizeBytes: file.SizeBytes}})
	if err != nil {
		return nil, err
	}
	names, fileManagement := p.namingFor(request.CategoryID)
	destRoot, err := p.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(basics.Title), file.SizeBytes)
	if err != nil {
		return nil, err
	}
	facts, err := NamingFacts(ctx, p.config.Reader, request.CategoryID, workID, now)
	if err != nil {
		return nil, err
	}
	facts.OriginalFilename = strings.TrimSuffix(path.Base(file.RelPath), path.Ext(file.RelPath))
	if info, ok := parseadult.ParseName(facts.OriginalFilename); ok {
		facts.ReleaseGroup = info.Group
	}
	destRel, _, err := names.RenderScene(facts, file.RelPath)
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
	oshash, _ := library.OSHash(p.config.FS, destAbs, file.SizeBytes)
	registered, err := p.config.Physical.RegisterBatch(ctx, destRoot.ID, []library.PhysicalFile{{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[0], OSHash: oshash, FirstSeenAt: now}})
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
	names := NamingFor(nil)
	fileManagement := settings.DefaultFileManagement
	if category, found := p.config.Categories.Category(categoryID); found {
		names = NamingFor(category.Naming)
		fileManagement = category.FileManagement
	}
	if p.config.RenameOnImport != nil && !p.config.RenameOnImport() {
		names.RenameFiles = false
	}
	return names, fileManagement
}

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
