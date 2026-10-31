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
	config ImporterConfig
	logger *slog.Logger
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Importer{config: config, logger: logger}
}

// MediaSizeFloorBytes implements batch.SizeFloor.
func (Importer) MediaSizeFloorBytes() int64 { return batch.VideoSizeFloorBytes }

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
)

var errNothingToPlace = errors.New("adult: the unit holds no file the scene still wants")

// rejectedError says the unit's file was refused by the import rules.
type rejectedError struct{ Rejections []model.Rejection }

func (e *rejectedError) Error() string {
	names := make([]string, len(e.Rejections))
	for index, rejection := range e.Rejections {
		names[index] = rejection.Rule
	}
	return "adult: refused by the import rules: " + strings.Join(names, ", ")
}

// ImportBatch implements batch.Importer.
func (i Importer) ImportBatch(ctx context.Context, request batch.Request) (batch.Result, error) {
	result := batch.Result{
		Decisions:  make([]library.UnitDecision, len(request.Units)),
		Placements: make([]*library.Placement, len(request.Units)),
	}
	var seeded int64
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgrade = i.seededScene(ctx, *request.AcquisitionID)
	}
	for index, unit := range request.Units {
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no scene of the library was found in it"}
		workID, candidates := seeded, []model.Identification(nil)
		if workID == 0 {
			workID, candidates = i.resolve(ctx, request, unit)
		}
		if workID <= 0 {
			result.Decisions[index] = decision
			result.Decisions[index].CandidatesJSON = library.CandidatesJSON(candidates)
			continue
		}
		decision.WorkID = &workID
		if request.Preview {
			decision.Reason = batch.ReasonPreview
			result.Decisions[index] = decision
			continue
		}
		placed, err := i.placeUnit(ctx, request, unit, workID, upgrade && workID == seeded)
		var refused *rejectedError
		switch {
		case errors.Is(err, errNothingToPlace):
			decision.Decision, decision.Reason, decision.WorkID = library.UnitSkip, batch.ReasonNothingNew, nil
			result.Decisions[index] = decision
		case errors.As(err, &refused):
			decision.Reason = refused.Error()
			result.Decisions[index] = decision
		case errors.Is(err, placer.ErrNoRoot):
			result.Decisions[index] = batch.WaitToPlace(decision, workID, err)
		case err != nil:
			i.logger.Warn("adult import: could not place unit", slog.String("unit", unit.Directory), slog.Any("error", err))
			decision.Decision, decision.Reason, decision.WorkID = library.UnitFailed, err.Error(), nil
			result.Decisions[index] = decision
		default:
			decision.Decision, decision.Reason, decision.Confidence = library.UnitCommit, "", 1
			result.Decisions[index] = decision
			result.Placements[index] = placed
			if i.config.AfterPlacing != nil {
				i.config.AfterPlacing(ctx, request.CategoryID, workID)
			}
		}
	}
	return result, nil
}

// resolve finds the scene a unit holds: from its file's fingerprint and name, else from the download's own name.
func (i Importer) resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	file := largest(unit.Media)
	if file == nil {
		return 0, nil
	}
	oshash, _ := library.OSHash(i.config.FS, path.Join(request.SourcePath, file.RelPath), file.SizeBytes)
	var candidates []model.Identification
	info, parsed := parseadult.ParsePath(file.RelPath)
	if parsed || oshash != "" {
		workID, found := i.config.Resolver.Resolve(ctx, request.CategoryID, info, oshash)
		if workID != 0 {
			return workID, nil
		}
		candidates = found
	}
	if release, ok := parseadult.ParseName(path.Base(path.Join(request.SourcePath, unit.Directory))); ok {
		if workID, found := i.config.Resolver.Resolve(ctx, request.CategoryID, release, ""); workID != 0 || len(candidates) == 0 {
			return workID, found
		}
	}
	return 0, candidates
}

// seededScene is the scene a download was grabbed for; upgrade says it was below its cutoff.
func (i Importer) seededScene(ctx context.Context, acquisitionID int64) (workID int64, upgrade bool) {
	acquisition, err := i.config.Acquisitions.Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return 0, false
	}
	item, err := i.config.Desired.Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return 0, false
	}
	return item.WorkID, item.State == works.StateUpgradable
}

// CommitHeld implements batch.HeldCommitter: the unit goes to the scene named, else the one it resolved to.
func (i Importer) CommitHeld(ctx context.Context, request batch.HeldRequest) (library.Placement, error) {
	workID := request.WorkID
	if request.As != nil && request.Guess > 0 && request.Guess < GuessFloor {
		return library.Placement{}, batch.ErrGuessTooWeak
	}
	if request.As != nil {
		ids, ok := NamedIDs(request.As.Provider, request.As.ExternalID)
		if !ok {
			return library.Placement{}, fmt.Errorf("%w: %s %q is not a scene id", media.ErrInvalidAdd, request.As.Provider, request.As.ExternalID)
		}
		if i.config.Exclusions != nil {
			if err := i.config.Exclusions.Allow(ctx, MediaType, request.As.Provider, request.As.ExternalID); err != nil {
				return library.Placement{}, err
			}
		}
		if workID = i.config.Resolver.ByIDs(ctx, request.CategoryID, ids, Hint{}); workID <= 0 {
			return library.Placement{}, batch.ErrUnresolved
		}
	}
	if workID <= 0 {
		return library.Placement{}, batch.ErrUnresolved
	}
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgradable := i.seededScene(ctx, *request.AcquisitionID)
		upgrade = upgradable && workID == seeded
	}
	placed, err := i.placeUnit(ctx, batch.Request{SessionID: request.SessionID, CategoryID: request.CategoryID, SourcePath: request.SourcePath}, request.Unit, workID, upgrade)
	if errors.Is(err, errNothingToPlace) {
		return library.Placement{}, batch.ErrNothingToPlace
	}
	if err != nil {
		return library.Placement{}, err
	}
	if i.config.AfterPlacing != nil {
		i.config.AfterPlacing(ctx, request.CategoryID, workID)
	}
	return *placed, nil
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
func (i Importer) screen(ctx context.Context, sourceAbs string, siblings []string, expectedMinutes int) *model.Rejection {
	for _, rejection := range []*model.Rejection{rules.Unpacking(sourceAbs, false), rules.MultiPart(sourceAbs, siblings)} {
		if rejection != nil {
			return rejection
		}
	}
	if i.config.Probe == nil {
		return nil
	}
	measured, ok := i.config.Probe(ctx, sourceAbs)
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
func (i Importer) placeUnit(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := i.config.Clock.Now()
	file := largest(unit.Media)
	if file == nil {
		return nil, errNothingToPlace
	}
	roots, err := i.config.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("scene %d: find existing roots: %w", workID, err)
	}
	if len(roots) > 0 && !upgrade {
		return nil, errNothingToPlace
	}
	basics, err := i.config.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("scene %d: %w", workID, err)
	}
	sourceAbs := path.Join(request.SourcePath, file.RelPath)
	siblings := make([]string, 0, len(unit.Media))
	for _, other := range unit.Media {
		siblings = append(siblings, path.Join(request.SourcePath, other.RelPath))
	}
	// A scene is as long as its provider says; the sample check scales to it.
	if rejection := i.screen(ctx, sourceAbs, siblings, int(basics.RuntimeMs/60_000)); rejection != nil {
		return nil, &rejectedError{Rejections: []model.Rejection{*rejection}}
	}
	hashes, err := i.config.Placer.CheckBlocked(ctx, request.SourcePath, []placer.File{{RelPath: file.RelPath, SizeBytes: file.SizeBytes}})
	if err != nil {
		return nil, err
	}
	names, fileManagement := i.namingFor(request.CategoryID)
	destRoot, err := i.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(basics.Title), file.SizeBytes)
	if err != nil {
		return nil, err
	}
	facts, err := NamingFacts(ctx, i.config.Reader, request.CategoryID, workID, now)
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
		destRel = i.config.Placer.FreeName(destRoot.Path, destRel)
	}
	destAbs := path.Join(destRoot.Path, destRel)
	methods := i.config.Placer.Methods(request.Ladder)
	method, err := mover.PlaceOne(i.config.FS, sourceAbs, destAbs, file.SizeBytes, methods)
	if err != nil {
		return nil, fmt.Errorf("place %q: %w", file.RelPath, err)
	}
	placed := []placer.Placed{{Source: sourceAbs, Dest: destAbs, Rel: destRel, Hash: hashes[0], Size: file.SizeBytes, Method: method}}
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, i.config.Placer.Undo(placed))
	}
	if method != mover.PlaceSymlink {
		i.config.Placer.Finish(destRoot, destAbs, fileManagement, time.Time{})
	}
	oshash, _ := library.OSHash(i.config.FS, destAbs, file.SizeBytes)
	registered, err := i.config.Physical.RegisterBatch(ctx, destRoot.ID, []library.PhysicalFile{{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[0], OSHash: oshash, FirstSeenAt: now}})
	if err != nil {
		return undo(fmt.Errorf("register placed file: %w", err))
	}
	if _, err := i.config.Series.AttachWholeFiles(ctx, now, workID, destRoot.ID, []int64{registered[0].MediaObjectID}); err != nil {
		return undo(err)
	}
	placement := &library.Placement{
		WorkID: workID, MediaObjectIDs: []int64{registered[0].MediaObjectID}, LibraryPathIDs: []int64{registered[0].LibraryPathID},
		TransferMethods: []string{string(method)},
	}
	if i.config.ExtraFiles != nil {
		owners := []placer.Owner{{SourceRel: file.RelPath, DestRel: destRel}}
		placement.Extras = i.config.Placer.Extras(i.config.ExtraFiles(request.CategoryID), request.SourcePath, destRoot, unit.Other, owners, methods)
	}
	return placement, nil
}

func (i Importer) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	names := NamingFor(nil)
	fileManagement := settings.DefaultFileManagement
	if category, found := i.config.Categories.Category(categoryID); found {
		names = NamingFor(category.Naming)
		fileManagement = category.FileManagement
	}
	if i.config.RenameOnImport != nil && !i.config.RenameOnImport() {
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
