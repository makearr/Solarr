package movie

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/makearr/Solarr/internal/importing/rules"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// defaultMovieMinutes is what Radarr assumes a film runs when nothing says.
const defaultMovieMinutes = 90

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
	// Exclusions lifts a movie's exclusion when a person names it for a held unit.
	Exclusions interface {
		Allow(ctx context.Context, mediaType, provider, externalID string) error
	}
	Clock          clock.Clock
	Logger         *slog.Logger
	RenameOnImport func() bool
	// ExtraFiles lists the extensions of files brought along with the film they are named after; nil brings none.
	ExtraFiles func(categoryID int64) []string
	// Probe measures a downloaded file for the sample and audio checks; nil skips those two checks.
	Probe func(ctx context.Context, absolutePath string) (measured mediatool.Measured, ok bool)
	// AfterPlacing is told of each movie an import placed a file of.
	AfterPlacing func(ctx context.Context, categoryID, workID int64)
}

// Importer is the movie module's batch importer: it works out which film a download folder holds, names and places its
// video file below the movie folder, and records it as a file of the film. What it cannot place waits in the Review queue.
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

// MediaSizeFloorBytes implements batch.SizeFloor: a sample or a stub is no film to import.
func (Importer) MediaSizeFloorBytes() int64 { return batch.VideoSizeFloorBytes }

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
)

var errNothingToPlace = errors.New("movie: the unit holds no file the film still wants")

// rejectedError says every video file of a unit was refused by Radarr's import rules.
type rejectedError struct{ Rejections []model.Rejection }

func (e *rejectedError) Error() string {
	names := make([]string, len(e.Rejections))
	for index, rejection := range e.Rejections {
		names[index] = rejection.Rule
	}
	return "movie: refused by the import rules: " + strings.Join(names, ", ")
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
		seeded, upgrade = i.seededMovie(ctx, *request.AcquisitionID)
	}
	for index, unit := range request.Units {
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no film of the library was found in it"}
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
			result.Decisions[index] = decision
		case errors.As(err, &refused):
			decision.Reason = refused.Error()
			result.Decisions[index] = decision
		case err != nil:
			i.logger.Warn("movie import: could not place unit", slog.String("unit", unit.Directory), slog.Any("error", err))
			decision.Decision, decision.Reason = library.UnitFailed, err.Error()
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

// resolve finds the film a unit holds from its folder and file names, and the ids an nfo beside the file names.
func (i Importer) resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (int64, []model.Identification) {
	file := largest(unit.Media)
	if file == nil {
		return 0, nil
	}
	info, ok := parsemovie.ParsePath(file.RelPath)
	if !ok {
		return 0, nil
	}
	return i.config.Resolver.Resolve(ctx, request.CategoryID, info, NFOIDs(i.config.FS, path.Join(request.SourcePath, file.RelPath)))
}

// seededMovie is the film a download was grabbed for; upgrade says it was below its cutoff, so the download may add a
// better version of what is held.
func (i Importer) seededMovie(ctx context.Context, acquisitionID int64) (workID int64, upgrade bool) {
	acquisition, err := i.config.Acquisitions.Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return 0, false
	}
	item, err := i.config.Desired.Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return 0, false
	}
	return item.WorkID, item.State == ItemStateUpgradable
}

// CommitHeld implements batch.HeldCommitter: the unit goes to the film named, else the one it resolved to.
func (i Importer) CommitHeld(ctx context.Context, request batch.HeldRequest) (library.Placement, error) {
	workID := request.WorkID
	if request.As != nil && request.Guess > 0 && request.Guess < GuessFloor {
		return library.Placement{}, batch.ErrGuessTooWeak
	}
	if request.As != nil {
		ids, err := namedIDs(request.As.Provider, request.As.ExternalID)
		if err != nil {
			return library.Placement{}, err
		}
		if i.config.Exclusions != nil {
			if err := i.config.Exclusions.Allow(ctx, MediaType, request.As.Provider, request.As.ExternalID); err != nil {
				return library.Placement{}, err
			}
		}
		if workID = i.config.Resolver.byID(ctx, request.CategoryID, ids); workID <= 0 {
			return library.Placement{}, batch.ErrUnresolved
		}
	}
	if workID <= 0 {
		return library.Placement{}, batch.ErrUnresolved
	}
	placed, err := i.placeUnit(ctx, batch.Request{SessionID: request.SessionID, CategoryID: request.CategoryID, SourcePath: request.SourcePath}, request.Unit, workID, false)
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

// largest is the unit's biggest video file, which is the film; the rest are extras and samples.
func largest(media []inspect.File) *inspect.File {
	var best *inspect.File
	for index := range media {
		if best == nil || media[index].SizeBytes > best.SizeBytes {
			best = &media[index]
		}
	}
	return best
}

// screen holds the film's file against the checks Radarr makes before it imports: still being unpacked, one disc of
// several, a sample by its runtime, no audio track. nil lets it through.
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
		expectedMinutes = defaultMovieMinutes
	}
	if verdict := rules.Sample(rules.SampleFacts{Path: sourceAbs, Runtime: measured.Duration, Probed: ok, ExpectedMinutes: expectedMinutes}); verdict == rules.VerdictSample {
		return rules.RejectSample(verdict, false)
	}
	// A file ffprobe could not read is let through, unlike Radarr, which waits: the tool is optional here.
	audioStreams := 0
	if measured.AudioCodec != "" {
		audioStreams = 1
	}
	return rules.HasAudio(ok && measured.VideoCodec != "", audioStreams)
}

func (i Importer) placeUnit(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := i.config.Clock.Now()
	file := largest(unit.Media)
	if file == nil {
		return nil, errNothingToPlace
	}
	// A second file is only for a film that wants a better one (Radarr's "existing file is better" rule).
	if roots, err := i.config.Identity.WorkFileRoots(ctx, workID); err == nil && len(roots) > 0 && !upgrade {
		return nil, errNothingToPlace
	}
	basics, err := i.config.Reader.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("movie %d: %w", workID, err)
	}
	sourceAbs := path.Join(request.SourcePath, file.RelPath)
	siblings := make([]string, 0, len(unit.Media))
	for _, other := range unit.Media {
		siblings = append(siblings, path.Join(request.SourcePath, other.RelPath))
	}
	if rejection := i.screen(ctx, sourceAbs, siblings, int(basics.RuntimeMs/60_000)); rejection != nil {
		return nil, &rejectedError{Rejections: []model.Rejection{*rejection}}
	}
	info, _ := parsemovie.ParsePath(file.RelPath)

	hashes, err := i.config.Placer.CheckBlocked(ctx, request.SourcePath, []placer.File{{RelPath: file.RelPath, SizeBytes: file.SizeBytes}})
	if err != nil {
		return nil, err
	}
	names, fileManagement := i.namingFor(request.CategoryID)
	destRoot, err := i.config.Placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(basics.Title, basics.Year), file.SizeBytes)
	if err != nil {
		return nil, err
	}
	ids, err := i.config.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return nil, err
	}
	destRel, _, err := names.RenderMovie(fileNaming(basics, ids, info, file), file.RelPath)
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

	registered, err := i.config.Physical.RegisterBatch(ctx, destRoot.ID, []library.PhysicalFile{{RelPath: destRel, SizeBytes: file.SizeBytes, QuickHash: hashes[0], FirstSeenAt: now}})
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
