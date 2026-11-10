package tv

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/importing/rules"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

type (
	// PlacementRoots is the settings-side facts placing a file needs.
	PlacementRoots = placer.Roots
	// PlacementAffinity keeps a series on one root once placed.
	PlacementAffinity = placer.Affinity
	// BlockedContent is the ADR-53 hash gate.
	BlockedContent = placer.Blocked
)

// AcquisitionSource finds the desired item a finished download was grabbed for.
type AcquisitionSource = batch.AcquisitionSource

// ImporterConfig is what an Importer needs; Logger, TransferLadder, RenameOnImport and GroupID are optional.
type ImporterConfig struct {
	FS           fsx.FS
	Roots        PlacementRoots
	Affinity     PlacementAffinity
	Physical     library.Physical
	Series       library.Series
	Reader       library.SeriesReader
	Identity     library.Identity
	Desired      library.Desired
	Acquisitions AcquisitionSource
	Resolver     Resolver
	Categories   interface {
		Category(id int64) (settings.Category, bool)
	}
	BlockedContent BlockedContent
	// Exclusions lifts a series' exclusion when a person names it for a held unit; nil lifts nothing.
	Exclusions interface {
		Allow(ctx context.Context, mediaType, provider, externalID string) error
	}
	Clock          clock.Clock
	Logger         *slog.Logger
	TransferLadder func() []mover.PlaceMethod
	// ExtraFiles lists the extensions of files brought along into a library with the episode they are named after; nil
	// brings none.
	ExtraFiles     func(categoryID int64) []string
	RenameOnImport func() bool
	GroupID        func(name string) (int, error)
	// BelowCutoff says an owned file misses its library's cutoff, so a download may replace it; nil lets any owned
	// file of an upgrading season be replaced.
	BelowCutoff func(categoryID int64, file library.EpisodeFile) bool
	// Probe measures a downloaded video file for the sample and audio checks; ok is false when it cannot be read. nil
	// skips those two checks.
	Probe func(ctx context.Context, absolutePath string) (measured mediatool.Measured, ok bool)
	// Fingerprint hashes a downloaded video's pictures and reads its length for the duplicate check; ok is false when it
	// cannot. nil skips the check.
	Fingerprint func(ctx context.Context, absolutePath string) (hash mediatool.VideoHash, durationMs int64, ok bool)
	// SameVideos are the files of a series that show the same pictures as a hash.
	SameVideos func(ctx context.Context, workID int64, hash mediatool.VideoHash, durationMs int64) ([]int64, error)
	// DuplicateCheck says whether the check is on; nil leaves it on.
	DuplicateCheck func() bool
	// AfterPlacing is told of each series an import placed episodes of; nil is told nothing.
	AfterPlacing func(ctx context.Context, categoryID, workID int64)
	// SharedFiles asks for the shared-broadcast pass after an imported episode turned out linked; nil never asks.
	SharedFiles func(ctx context.Context) error
}

// Importer is the TV module's batch importer: it works out which episodes a folder of video files holds, names and
// places each file below the series folder, and records which episodes it covers. What it cannot place waits for a
// person in the Review queue.
type Importer struct {
	config ImporterConfig
	logger *slog.Logger
	placer placer.Placer
}

// NewImporter builds the importer.
func NewImporter(config ImporterConfig) Importer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Importer{config: config, logger: logger, placer: placer.New(placer.Config{
		FS: config.FS, Roots: config.Roots, Affinity: config.Affinity, Identity: config.Identity, Blocked: config.BlockedContent,
		Clock: config.Clock, Logger: logger, TransferLadder: config.TransferLadder, GroupID: config.GroupID,
	})}
}

// MediaSizeFloorBytes implements batch.SizeFloor: a sample or a stub is no episode to import.
func (Importer) MediaSizeFloorBytes() int64 { return batch.VideoSizeFloorBytes }

var _ batch.Importer = Importer{}

// ImportBatch implements batch.Importer.
func (i Importer) ImportBatch(ctx context.Context, request batch.Request) (batch.Result, error) {
	result := batch.Result{
		Decisions:  make([]library.UnitDecision, len(request.Units)),
		Placements: make([]*library.Placement, len(request.Units)),
	}
	var seeded int64
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgrade = i.seedSeries(ctx, *request.AcquisitionID)
	}
	for index, unit := range request.Units {
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no episode of a known series was found in it"}
		workID, candidates := seeded, []model.Identification(nil)
		if workID == 0 {
			folder := path.Base(request.SourcePath)
			if unit.Directory != "" {
				folder = path.Base(unit.Directory)
			}
			named := ShowNFOIDs(i.config.FS, path.Join(request.SourcePath, unit.Directory))
			workID, candidates = i.config.Resolver.ResolveFolder(ctx, request.CategoryID, folder, []string{firstMedia(unit)}, named)
		}
		if workID <= 0 {
			result.Decisions[index] = decision
			result.Decisions[index].CandidatesJSON = library.CandidatesJSON(candidates)
			continue
		}
		if request.Preview {
			decision.Reason = batch.ReasonPreview
			decision.WorkID = &workID
			result.Decisions[index] = decision
			continue
		}
		placed, err := i.placeUnit(ctx, request, unit, workID, upgrade && workID == seeded)
		var refused *batch.RejectedError
		var untitled *titleWaitError
		switch {
		case errors.Is(err, errNothingToPlace):
			decision.Decision, decision.Reason = library.UnitSkip, batch.ReasonNothingNew
			result.Decisions[index] = decision
		case errors.Is(err, errDuplicate):
			decision.Decision, decision.Reason = library.UnitSkip, batch.ReasonDuplicate
			result.Decisions[index] = decision
		case errors.As(err, &refused):
			// Held for a person with the rule that refused it, as Sonarr leaves such a file in the download folder.
			decision.WorkID = &workID
			decision.Reason = refused.Error()
			result.Decisions[index] = decision
		case errors.Is(err, placer.ErrNoRoot), errors.As(err, &untitled):
			result.Decisions[index] = batch.WaitToPlace(decision, workID, err)
		case err != nil:
			i.logger.Warn("tv import: could not place unit", slog.String("unit", unit.Directory), slog.Any("error", err))
			decision.Decision, decision.Reason = library.UnitFailed, err.Error()
			result.Decisions[index] = decision
		default:
			decision.Decision, decision.Reason, decision.Confidence = library.UnitCommit, "", 1
			decision.WorkID = &workID
			result.Decisions[index] = decision
			result.Placements[index] = placed
			i.afterPlacing(ctx, request.CategoryID, workID)
		}
	}
	return result, nil
}

var _ batch.HeldCommitter = Importer{}

// ErrNotASeriesID is a named work whose id is not one of a series source.
var ErrNotASeriesID = errors.New("tv: that is not a series id")

// CommitHeld implements batch.HeldCommitter: the unit goes to the series named, else the one it resolved to.
func (i Importer) CommitHeld(ctx context.Context, request batch.HeldRequest) (library.Placement, error) {
	workID := request.WorkID
	if request.As != nil && request.Guess > 0 && request.Guess < GuessFloor {
		return library.Placement{}, batch.ErrGuessTooWeak
	}
	if request.As != nil {
		id, err := strconv.ParseInt(request.As.ExternalID, 10, 64)
		if err != nil || id <= 0 {
			return library.Placement{}, fmt.Errorf("%w: %q", ErrNotASeriesID, request.As.ExternalID)
		}
		ids := IDs{TVDB: id}
		if request.As.Provider == ProviderTMDB {
			ids = IDs{TMDB: id}
		}
		if i.config.Exclusions != nil {
			if err := i.config.Exclusions.Allow(ctx, MediaType, request.As.Provider, request.As.ExternalID); err != nil {
				return library.Placement{}, err
			}
		}
		resolver := i.config.Resolver
		workID, err = findOrAddSeries(ctx, resolver.Service, resolver.Categories, resolver.DefaultProfileID, request.CategoryID, ids)
		if err != nil {
			return library.Placement{}, err
		}
	}
	if workID <= 0 {
		return library.Placement{}, batch.ErrUnresolved
	}
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgradable := i.seedSeries(ctx, *request.AcquisitionID)
		upgrade = upgradable && workID == seeded
	}
	placed, err := i.placeUnit(ctx, batch.Request{
		SessionID: request.SessionID, CategoryID: request.CategoryID, SourcePath: request.SourcePath,
	}, request.Unit, workID, upgrade)
	if errors.Is(err, errNothingToPlace) || errors.Is(err, errDuplicate) {
		return library.Placement{}, batch.ErrNothingToPlace
	}
	if err != nil {
		return library.Placement{}, err
	}
	i.afterPlacing(ctx, request.CategoryID, workID)
	return *placed, nil
}

func (i Importer) afterPlacing(ctx context.Context, categoryID, workID int64) {
	if i.config.AfterPlacing != nil {
		i.config.AfterPlacing(ctx, categoryID, workID)
	}
}

func firstMedia(unit inspect.Unit) string {
	if len(unit.Media) > 0 {
		return unit.Media[0].RelPath
	}
	return ""
}

// seedSeries is the series a download was grabbed for: its desired item's work; upgrade says the item was below its
// cutoff, so the download is meant to replace files it already has.
func (i Importer) seedSeries(ctx context.Context, acquisitionID int64) (workID int64, upgrade bool) {
	acquisition, err := i.config.Acquisitions.Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return 0, false
	}
	item, err := i.config.Desired.Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return 0, false
	}
	// A season the cutoff job has not marked yet is still COMPLETE; its below-cutoff episodes stay replaceable by the per-episode check.
	return item.WorkID, item.State == ItemStateUpgradable || item.State == "PARTIAL" || (item.State == ItemStateComplete && i.config.BelowCutoff != nil)
}

// screen holds one downloaded file against the checks Sonarr makes before it imports: still being unpacked, a whole
// season or one piece of an episode in a single file, a sample by its runtime, no audio track. nil lets it through.
func (i Importer) screen(ctx context.Context, sourceAbs string, info parsetv.Info, parts []library.ResolvedPart, runtimes map[string]int64) *model.Rejection {
	for _, rejection := range []*model.Rejection{rules.Unpacking(sourceAbs, false), rules.FullSeason(info.FullSeason), rules.SplitEpisode(info.SplitEpisode)} {
		if rejection != nil {
			return rejection
		}
	}
	if i.config.Probe == nil {
		return nil
	}
	measured, ok := i.config.Probe(ctx, sourceAbs)
	expected := 0
	for _, part := range parts {
		expected += int(runtimes[part.MediaRef] / 60_000)
	}
	if expected == 0 {
		expected = defaultEpisodeMinutes
	}
	verdict := rules.Sample(rules.SampleFacts{Path: sourceAbs, Runtime: measured.Duration, Probed: ok, ExpectedMinutes: expected, Special: info.Special})
	if verdict == rules.VerdictSample {
		return rules.RejectSample(verdict, false)
	}
	// A file ffprobe could not read is let through, unlike Sonarr, which waits: the tool is optional here.
	audioStreams := 0
	if measured.AudioCodec != "" {
		audioStreams = 1
	}
	return rules.HasAudio(ok && measured.VideoCodec != "", audioStreams)
}

// defaultEpisodeMinutes is what Sonarr assumes an episode runs when nothing says.
const defaultEpisodeMinutes = 45

var errNothingToPlace = errors.New("tv: no file of the unit covers an episode that is wanted")

// errDuplicate is a unit whose files all show pictures the series holds already, in a file that needs no replacing.
var errDuplicate = errors.New("tv: every file of the unit is a copy of one the series holds")

// fileEpisodes is one video file and the episodes it covers.
type fileEpisodes struct {
	media inspect.File
	info  parsetv.Info
	parts []library.ResolvedPart
	// replacing says the file is for an episode that already has one.
	replacing bool
	// hash is the file's pictures when the duplicate check read them, kept with the placed file for the next download.
	hash *mediatool.VideoHash
}

func (i Importer) placeUnit(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error) {
	now := i.config.Clock.Now()
	detail, err := i.config.Reader.Get(ctx, request.CategoryID, workID, now)
	if err != nil {
		return nil, fmt.Errorf("series %d: %w", workID, err)
	}
	present := map[string]bool{}
	replaceable := map[string]bool{}
	for _, episode := range detail.Episodes {
		if episode.Present {
			ref := MediaRef(episode.Season, episode.Number)
			present[ref] = true
			replaceable[ref] = upgrade && episode.File != nil && (i.config.BelowCutoff == nil || i.config.BelowCutoff(request.CategoryID, *episode.File))
		}
	}

	runtimes := map[string]int64{}
	for _, episode := range detail.Episodes {
		runtimes[MediaRef(episode.Season, episode.Number)] = episode.RuntimeMs
	}
	scene := newSceneNumbers(detail.Episodes)
	var files []fileEpisodes
	var rejections []model.Rejection
	var totalBytes int64
	duplicates := 0
	for _, media := range unit.Media {
		info, ok := parsetv.ParsePath(media.RelPath, now)
		if !ok {
			continue
		}
		parts, _, err := i.config.Series.ResolveParts(ctx, workID, scene.partKeys(info))
		if err != nil {
			return nil, err
		}
		// The picture says which episode a file is, whatever its release named it.
		sourceAbs := path.Join(request.SourcePath, media.RelPath)
		replaceableHere := replaceable
		hash, copied, err := i.heldCopy(ctx, request.CategoryID, workID, sourceAbs, detail.Episodes)
		if err != nil {
			return nil, err
		}
		if copied != nil {
			i.logger.Info("tv import: file is a copy of an episode the series holds", slog.String("file", media.RelPath), slog.Any("episodes", copied.refs))
			if !copied.replaceable {
				duplicates++
				continue
			}
			parts = copied.parts
			replaceableHere = map[string]bool{}
			for _, part := range parts {
				replaceableHere[part.MediaRef] = true
			}
		}
		// An upgrade brings a second file for an episode below the cutoff; the cutoff check keeps the better one once both
		// are measured.
		fresh := slices.DeleteFunc(slices.Clone(parts), func(part library.ResolvedPart) bool { return present[part.MediaRef] && !replaceableHere[part.MediaRef] })
		if len(fresh) == 0 {
			continue
		}
		replacing := slices.ContainsFunc(fresh, func(part library.ResolvedPart) bool { return present[part.MediaRef] })
		if rejection := i.screen(ctx, sourceAbs, info, fresh, runtimes); rejection != nil {
			rejections = append(rejections, *rejection)
			continue
		}
		files = append(files, fileEpisodes{media: media, info: info, parts: fresh, replacing: replacing, hash: hash})
		totalBytes += media.SizeBytes
	}
	if len(files) == 0 {
		if len(rejections) > 0 {
			return nil, &batch.RejectedError{MediaType: "tv", Rejections: rejections}
		}
		if duplicates > 0 {
			return nil, errDuplicate
		}
		return nil, errNothingToPlace
	}

	names, fileManagement := i.namingFor(request.CategoryID)
	if err := titleWait(fileManagement.EpisodeTitleRequired, files, airedAt(detail.Episodes), now); err != nil {
		return nil, err
	}

	toCheck := make([]placer.File, len(files))
	for index, file := range files {
		toCheck[index] = placer.File{RelPath: file.media.RelPath, SizeBytes: file.media.SizeBytes}
	}
	hashes, err := i.placer.CheckBlocked(ctx, request.SourcePath, toCheck)
	if err != nil {
		return nil, err
	}
	destRoot, err := i.placer.ChooseRoot(ctx, request.CategoryID, workID, affinityKey(detail.Summary.Title), totalBytes)
	if err != nil {
		return nil, err
	}
	methods := i.placer.Methods(request.Ladder)

	placed := make([]placedEpisode, 0, len(files))
	undo := func(cause error) (*library.Placement, error) {
		return nil, errors.Join(cause, i.placer.Undo(placedFiles(placed)))
	}
	for index, file := range files {
		naming := episodeNaming(detail, file)
		destRel, _, err := names.RenderEpisode(naming, file.media.RelPath)
		if err != nil {
			return undo(fmt.Errorf("name %q: %w", file.media.RelPath, err))
		}
		sourceAbs := path.Join(request.SourcePath, file.media.RelPath)
		if file.replacing {
			destRel = i.placer.FreeName(destRoot.Path, destRel)
		}
		destAbs := path.Join(destRoot.Path, destRel)
		method, err := mover.PlaceOneOnDisk(ctx, i.config.FS, sourceAbs, destAbs, file.media.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("place %q: %w", file.media.RelPath, err))
		}
		placed = append(placed, placedEpisode{Placed: placer.Placed{Source: sourceAbs, Dest: destAbs, Size: file.media.SizeBytes, Method: method, Rel: destRel, Hash: hashes[index]}, file: file})
		if method != mover.PlaceSymlink {
			i.placer.Finish(destRoot, destAbs, fileManagement, firstAirDate(file.parts))
		}
	}

	physical := make([]library.PhysicalFile, len(placed))
	for index, p := range placed {
		physical[index] = library.PhysicalFile{RelPath: p.Rel, SizeBytes: p.Size, QuickHash: p.Hash, FirstSeenAt: now}
	}
	registered, err := i.config.Physical.RegisterBatch(ctx, destRoot.ID, physical)
	if err != nil {
		return undo(fmt.Errorf("register placed files: %w", err))
	}
	for index, p := range placed {
		if p.file.hash != nil {
			i.keepHash(ctx, registered[index].MediaObjectID, *p.file.hash)
		}
	}
	links := make([]library.SeriesFileLink, len(placed))
	for index, p := range placed {
		keys := make([]library.PartKey, len(p.file.parts))
		for k, part := range p.file.parts {
			keys[k] = library.PartKey{MediaRef: part.MediaRef}
		}
		links[index] = library.SeriesFileLink{MediaObjectID: registered[index].MediaObjectID, Parts: keys}
	}
	attachment, err := i.config.Series.AttachFiles(ctx, now, workID, destRoot.ID, links)
	if err != nil {
		return undo(err)
	}
	if attachment.Shared > 0 && i.config.SharedFiles != nil {
		if err := i.config.SharedFiles(ctx); err != nil {
			i.logger.Warn("tv import: shared episodes pass not queued", slog.Any("error", err))
		}
	}

	placement := &library.Placement{WorkID: workID, Extras: i.placeExtras(request.CategoryID, request.SourcePath, destRoot, unit, placed, methods)}
	for index, p := range placed {
		placement.MediaObjectIDs = append(placement.MediaObjectIDs, registered[index].MediaObjectID)
		placement.LibraryPathIDs = append(placement.LibraryPathIDs, registered[index].LibraryPathID)
		placement.TransferMethods = append(placement.TransferMethods, string(p.Method))
	}
	return placement, nil
}

// keepHash stores a placed file's pictures so the next download is compared with it; a failure only costs that.
func (i Importer) keepHash(ctx context.Context, mediaObjectID int64, hash mediatool.VideoHash) {
	frames := make([]library.FrameHash, len(hash.Frames))
	for k, frame := range hash.Frames {
		frames[k] = library.FrameHash{Slot: frame.Slot, Hash: frame.Hash}
	}
	if err := i.config.Physical.SetPerceptualHash(ctx, mediaObjectID, hash.Mean, frames); err != nil {
		i.logger.Warn("tv import: could not keep a file's pictures", slog.Int64("media_object_id", mediaObjectID), slog.Any("error", err))
	}
}

// copyOf is the episodes of the series a downloaded file turned out to be, and whether it may replace the file that holds them.
type copyOf struct {
	refs        []string
	parts       []library.ResolvedPart
	replaceable bool
}

// heldCopy reads a downloaded file's pictures and finds the file of the series that shows the same; nil when the check
// is off, cannot read the file or finds none. hash is the file's pictures whenever they were read.
func (i Importer) heldCopy(ctx context.Context, categoryID, workID int64, sourceAbs string, episodes []library.SeriesEpisode) (*mediatool.VideoHash, *copyOf, error) {
	if i.config.Fingerprint == nil || i.config.SameVideos == nil || (i.config.DuplicateCheck != nil && !i.config.DuplicateCheck()) {
		return nil, nil, nil
	}
	hash, durationMs, ok := i.config.Fingerprint(ctx, sourceAbs)
	if !ok {
		return nil, nil, nil
	}
	same, err := i.config.SameVideos(ctx, workID, hash, durationMs)
	if err != nil {
		return nil, nil, err
	}
	for _, held := range same {
		var refs []string
		var file *library.EpisodeFile
		for _, episode := range episodes {
			if episode.File != nil && episode.File.MediaObjectID == held {
				refs = append(refs, MediaRef(episode.Season, episode.Number))
				file = episode.File
			}
		}
		if file == nil {
			continue
		}
		keys := make([]library.PartKey, len(refs))
		for k, ref := range refs {
			keys[k] = library.PartKey{MediaRef: ref}
		}
		parts, _, err := i.config.Series.ResolveParts(ctx, workID, keys)
		if err != nil {
			return nil, nil, err
		}
		below := i.config.BelowCutoff == nil || i.config.BelowCutoff(categoryID, *file)
		return &hash, &copyOf{refs: refs, parts: parts, replaceable: below}, nil
	}
	return &hash, nil, nil
}

// placeExtras brings along the unit's other files that are named after a placed episode file (package placer).
func (i Importer) placeExtras(
	categoryID int64, sourceRoot string, destRoot settings.RootFolder, unit inspect.Unit, placed []placedEpisode, methods []mover.PlaceMethod,
) []library.UnitExtra {
	if i.config.ExtraFiles == nil {
		return nil
	}
	owners := make([]placer.Owner, len(placed))
	for index, one := range placed {
		owners[index] = placer.Owner{SourceRel: one.file.media.RelPath, DestRel: one.Rel}
	}
	return i.placer.Extras(i.config.ExtraFiles(categoryID), sourceRoot, destRoot, unit.Other, owners, methods)
}

func placedFiles(placed []placedEpisode) []placer.Placed {
	out := make([]placer.Placed, len(placed))
	for index, p := range placed {
		out[index] = p.Placed
	}
	return out
}

// PlacementKey is the affinity key a series' title places under, for a root chosen before its first file.
func PlacementKey(title string) string { return affinityKey(title) }

func affinityKey(title string) string {
	if key := "series:" + parsetv.CleanTitle(title); key != "series:" {
		return key
	}
	return ""
}

// firstAirDate is the air date of the first of parts that has one, zero when none does.
func firstAirDate(parts []library.ResolvedPart) time.Time {
	for _, part := range parts {
		if at, ok := airInstant(part.Date); ok {
			return at
		}
	}
	return time.Time{}
}

type placedEpisode struct {
	placer.Placed
	file fileEpisodes
}

func (i Importer) namingFor(categoryID int64) (Naming, settings.FileManagement) {
	profile, fileManagement, mayRename := works.CategoryNaming(i.config.Categories, categoryID, i.config.RenameOnImport)
	names := NamingFor(profile)
	names.RenameFiles = names.RenameFiles && mayRename
	return names, fileManagement
}

// episodeNaming gathers everything a placed file's name can draw on.
func episodeNaming(detail library.SeriesDetailRow, file fileEpisodes) EpisodeNaming {
	summary := detail.Summary
	seriesType := SeriesType(summary.Fields["series_type"])
	if !seriesType.Valid() {
		seriesType = SeriesStandard
	}
	naming := EpisodeNaming{
		SeriesType: seriesType, NoSeasonFolder: summary.Fields[FieldSeasonFolder] == SeasonFoldersOff, SeriesTitle: summary.Title, SeriesYear: int(summary.Year),
		IDs:          idsOf(detail.IDs),
		QualityTitle: releasefacts.QualityTitle(file.info.Claims), Proper: file.info.Claims.Version > 1, Version: file.info.Claims.Version,
		VideoCodec: strings.ToUpper(file.info.Claims.VideoCodec), DynamicRange: file.info.Claims.HDR,
		AudioCodec: strings.ToUpper(file.info.Claims.AudioCodec), AudioChannels: file.info.Claims.AudioChannels,
		ReleaseGroup: file.info.ReleaseGroup, OriginalTitle: file.info.ReleaseTitle,
		OriginalFilename: strings.TrimSuffix(path.Base(file.media.RelPath), path.Ext(file.media.RelPath)),
	}
	parts := slices.Clone(file.parts)
	slices.SortFunc(parts, func(a, b library.ResolvedPart) int { return cmp.Compare(a.MediaRef, b.MediaRef) })
	for _, part := range parts {
		season, number := splitRef(part.MediaRef)
		naming.Season = season
		naming.Episodes = append(naming.Episodes, EpisodeNumbering{Number: number, Title: part.Title})
		if naming.AirDate == "" {
			naming.AirDate = part.Date
		}
	}
	if file.info.AirDate != "" {
		naming.SeriesType = SeriesDaily
		naming.AirDate = file.info.AirDate
	}
	return naming
}

func splitRef(ref string) (season, episode int) {
	head, tail, _ := strings.Cut(ref, ":")
	season, _ = strconv.Atoi(strings.TrimPrefix(head, "s"))
	episode, _ = strconv.Atoi(strings.TrimPrefix(tail, "e"))
	return season, episode
}

func idsOf(ids map[string]string) IDs {
	var out IDs
	out.TVDB, _ = strconv.ParseInt(ids[ProviderTVDB], 10, 64)
	out.TVmaze, _ = strconv.ParseInt(ids[ProviderTVmaze], 10, 64)
	out.TMDB, _ = strconv.ParseInt(ids[ProviderTMDB], 10, 64)
	out.IMDb = ids[ProviderIMDb]
	return out
}
