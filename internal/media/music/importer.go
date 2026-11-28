package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// UnitCommitter places one identified unit into the library: Committer's own shape, an interface so a test can fake it.
type UnitCommitter interface {
	CommitMatchedUnit(
		ctx context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, tags Resolved,
		categoryID int64, matched *AlbumPlacement, ladder []string,
	) (Placed, error)
}

// UnverifiedCommitter is a UnitCommitter that also adds what nothing identified as an unverified album, and moves
// one onto the album later chosen for it (D6).
type UnverifiedCommitter interface {
	CommitUnverifiedUnit(ctx context.Context, sourceRoot string, unit inspect.Unit, tags Resolved, categoryID int64, ladder []string) (Placed, error)
	IdentifyUnverified(ctx context.Context, fromWorkID int64, best model.Identification, categoryID int64) (Placed, error)
}

// ImporterConfig is what an Importer needs; Tool is required. Without a Committer units are identified and placed
// nowhere; Search, Albums, AcoustID and Provenance each add a rung of identification when set.
type ImporterConfig struct {
	Tool     mediatool.Tool
	Search   Searcher
	Albums   AlbumLookup
	AcoustID AcoustIDLookup
	// Videos names the videos of a download from MusicBrainz; nil leaves them unverified.
	Videos VideoLookup
	// WantedVideo answers the video work a download was grabbed for, nil when it was grabbed for none.
	WantedVideo func(ctx context.Context, acquisitionID int64) (*VideoIdentity, error)
	// Kinds answers a library's kind (Concert videos, Music videos); nil for none.
	Kinds     func(categoryID int64) string
	Committer UnitCommitter
	// Provenance answers the release groups a download was grabbed for, which album identification always scores.
	Provenance func(ctx context.Context, acquisitionID int64) ([]string, error)
	// Profile holds a download that named no format to what the probe finds; nil trusts every release.
	Profile *ProfileGate
	Logger  *slog.Logger
}

// ProfileGate is the check at import of a release that claimed no format: the probe decides against the library's profiles.
type ProfileGate struct {
	// Claimless says the release an acquisition was grabbed as names no format, so nothing but the probe tells what it is.
	Claimless func(ctx context.Context, acquisitionID int64) bool
	// Accepts says a probed file passes one of the library's quality profiles; known is false when it has none to judge by.
	Accepts func(categoryID int64, facts score.Facts) (accepts, known bool)
}

// Importer is Lidarr's import behind the import job's batch contract: identify each unit from its tags, its album
// and its audio, decide, and place what clears the session's threshold.
type Importer struct {
	config ImporterConfig
	logger *slog.Logger
	// inspected holds the probes the inspection read, until the batch that follows takes them.
	inspected *probeMemo
}

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
	_ batch.TagEvidence   = Importer{}
)

// NewImporter builds one.
func NewImporter(config ImporterConfig) Importer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Importer{config: config, logger: logger, inspected: &probeMemo{}}
}

// InspectTags implements batch.TagEvidence: the tags of the audio files among relPaths, read in one batch.
func (i Importer) InspectTags(ctx context.Context, root string, relPaths []string) (inspect.TagGrouper, error) {
	audio := make([]string, 0, len(relPaths))
	for _, relPath := range relPaths {
		if filekind.Of(relPath) == filekind.Audio {
			audio = append(audio, relPath)
		}
	}
	if len(audio) == 0 {
		return nil, nil
	}
	unreadable := 0
	remember := func(absolute string, probe mediatool.Probe) {
		if probe.Err != nil {
			unreadable++
		}
		i.inspected.put(absolute, probe)
	}
	grouper, err := buildInspectTagGrouper(ctx, i.config.Tool, root, audio, remember)
	if unreadable > 0 {
		i.logger.Warn("music: files whose tags could not be read stay untagged for grouping", "root", root, "files", unreadable)
	}
	return grouper, err
}

// probesFor reads the tags of the files, asking the tool only for those the inspection did not already read.
func (i Importer) probesFor(ctx context.Context, absolute []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(absolute))
	var missing []string
	var missingAt []int
	for index, one := range absolute {
		if probe, found := i.inspected.take(one); found {
			probes[index] = probe
			continue
		}
		missing = append(missing, one)
		missingAt = append(missingAt, index)
	}
	if len(missing) == 0 {
		return probes, nil
	}
	read, err := i.config.Tool.ReadTags(ctx, missing)
	if err != nil {
		return nil, err
	}
	for k, at := range missingAt {
		if k < len(read) {
			probes[at] = read[k]
		}
	}
	return probes, nil
}

// unreadTags is the tag source of the name stage: it has read nothing, so the search identifier falls back to the folder.
type unreadTags struct{}

func (unreadTags) TagsFor(context.Context, model.FileRef) (mediatool.Tags, error) { return nil, nil }

// NameMatches implements batch.Importer: the album a unit's folder names, searched through the same identifier the
// files go through but fed no tags, so nothing of the audio is read.
func (i Importer) NameMatches(ctx context.Context, request batch.Request) []batch.NameMatch {
	matches := make([]batch.NameMatch, len(request.Units))
	if i.config.Search == nil {
		return matches
	}
	ctx = musicbrainz.ForLibrary(ctx, request.CategoryID)
	identifiers := []model.Identifier{NewSearchIdentifier(SearchIdentifierConfig{Tags: unreadTags{}, Search: i.config.Search})}
	cfg := model.ChainConfig{AcceptThreshold: DefaultAcceptThreshold, AmbiguityThreshold: request.Threshold}
	batch.ResolveAll(ctx, len(request.Units), func(ctx context.Context, index int) {
		unit := request.Units[index]
		if len(unit.Media) == 0 {
			return
		}
		named := unit
		named.Media = unit.Media[:1]
		result, err := IdentifyUnit(ctx, named, identifiers, cfg)
		if err != nil || result.Best == nil || result.Best.ExternalID == nil || result.Best.Confidence < batch.NameMatchFloor {
			return
		}
		matches[index] = batch.NameMatch{
			External: result.Best.ExternalID, Title: nameMatchTitle(*result.Best),
			Confidence: result.Best.Confidence, Candidates: []model.Identification{*result.Best},
		}
	})
	return matches
}

// nameMatchTitle is "Artist - Album" as the search read it.
func nameMatchTitle(best model.Identification) string {
	var artist, title string
	for _, evidence := range best.Evidence {
		switch evidence.Kind {
		case EvidenceArtist:
			artist = evidence.Detail
		case EvidenceTitle:
			title = evidence.Detail
		}
	}
	return combinedTitle(artist, title)
}

// identifyParallelism bounds IDENTIFY's fan-out across one batch's units; writes stay with the import job.
const identifyParallelism = 8

// ImportBatch implements batch.Importer.
func (i Importer) ImportBatch(ctx context.Context, request batch.Request) (batch.Result, error) {
	// The import is looked up in its library's provider order; one deciding per unit has none yet and takes the default.
	ctx = musicbrainz.ForLibrary(ctx, request.CategoryID)
	var relPaths []string
	for _, unit := range request.Units {
		for _, f := range unit.Media {
			relPaths = append(relPaths, f.RelPath)
		}
	}
	absolute := make([]string, len(relPaths))
	for index, rel := range relPaths {
		absolute[index] = path.Join(request.SourcePath, rel)
	}
	probes, err := i.probesFor(ctx, absolute)
	if err != nil {
		return batch.Result{}, fmt.Errorf("music: probe batch: %w", err)
	}
	tags := UnitTagSourceFor(request.SourcePath, probes)
	cfg := model.ChainConfig{AcceptThreshold: DefaultAcceptThreshold, AmbiguityThreshold: request.Threshold}
	refine := i.albumRefiner(request.SourcePath, relPaths, probes, request.Threshold, i.provenanceSeeds(ctx, request.AcquisitionID))

	decisions := make([]library.UnitDecision, len(request.Units))
	results := make([]model.ChainResult, len(request.Units))
	matched := make([]*AlbumPlacement, len(request.Units))
	videoIdentities := make([][]*VideoIdentity, len(request.Units))
	identifiers := i.identifiers(request.SourcePath, tags)
	identified := parallel.Do(ctx, len(request.Units), identifyParallelism, func(ctx context.Context, index int) error {
		if isVideoUnit(request.Units[index]) {
			decisions[index] = i.decideVideoUnit(ctx, request, index, relPaths, probes, videoIdentities, cfg.AmbiguityThreshold)
			return nil
		}
		decisions[index], results[index], matched[index] = identifyUnit(ctx, request.UnitIDs[index], request.Units[index], identifiers, cfg, refine)
		return nil
	})
	for index, err := range identified {
		if err != nil {
			// One bad unit does not fail the batch, a panicking one included.
			decisions[index] = library.UnitDecision{UnitID: request.UnitIDs[index], Decision: string(model.DecisionFailed)}
		}
	}
	if request.Preview {
		holdForReview(decisions)
	}

	for index, decision := range decisions {
		if decision.Decision == library.UnitCommit && results[index].Best != nil &&
			request.NameOf(index).Disagrees(0, results[index].Best.ExternalID) {
			decisions[index].Decision, decisions[index].Reason = library.UnitQueue, batch.ReasonNameDisagrees
		}
	}

	placements := make([]*library.Placement, len(request.Units))
	if request.CategoryID == 0 || i.config.Committer == nil {
		return batch.Result{Decisions: decisions, Placements: placements}, nil
	}
	claimless := i.claimless(ctx, request)
	for index, decision := range decisions {
		if decision.Decision == library.UnitCommit && isVideoUnit(request.Units[index]) {
			placed, err := i.commitVideoUnit(ctx, request, index, relPaths, probes, videoIdentities[index])
			if err != nil {
				i.logger.Warn("import commit: could not place video unit", slog.Int64("unit_id", request.UnitIDs[index]), slog.Any("error", err))
				decisions[index].Decision, decisions[index].Reason = library.UnitFailed, err.Error()
				continue
			}
			placements[index] = i.placement(request.UnitIDs[index], placed)
			continue
		}
		if decision.Decision != library.UnitCommit || results[index].Best == nil {
			continue
		}
		if claimless && !i.probedFilesFit(request, request.Units[index], relPaths, probes) {
			decisions[index] = library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitSkip, Reason: library.UnitReasonBelowProfile}
			continue
		}
		placed, err := i.config.Committer.CommitMatchedUnit(ctx, request.SourcePath, request.Units[index], *results[index].Best,
			tags, request.CategoryID, matched[index], request.Ladder)
		if errors.Is(err, ErrBlockedContent) {
			// ADR-53 part 2: a blocked hash is loud, not a unit decided but silently unplaced.
			decisions[index] = library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitSkip, Reason: err.Error()}
			continue
		}
		if errors.Is(err, ErrNoPlacement) {
			// Decided but unplaced: the download is kept and the import retry places it once a root can take it.
			decisions[index].Reason = err.Error()
			continue
		}
		if err != nil {
			i.logger.Warn("import commit: could not place unit", slog.Int64("unit_id", request.UnitIDs[index]),
				slog.String("path", request.Units[index].Directory), slog.Any("error", err))
			decisions[index].Decision, decisions[index].Reason = library.UnitFailed, err.Error()
			continue
		}
		placements[index] = i.placement(request.UnitIDs[index], placed)
	}
	if unverified, ok := i.config.Committer.(UnverifiedCommitter); ok && request.AddUnidentified {
		for index, decision := range decisions {
			if decision.Decision != library.UnitQueue || len(request.Units[index].Media) == 0 {
				continue
			}
			var placed Placed
			var err error
			if isVideoUnit(request.Units[index]) {
				placed, err = i.commitVideoUnit(ctx, request, index, relPaths, probes, videoIdentities[index])
			} else {
				placed, err = unverified.CommitUnverifiedUnit(ctx, request.SourcePath, request.Units[index], tags, request.CategoryID, request.Ladder)
			}
			if err != nil {
				i.logger.Warn("import: could not add an unverified album", slog.Int64("unit_id", request.UnitIDs[index]),
					slog.String("path", request.Units[index].Directory), slog.Any("error", err))
				continue
			}
			placements[index] = i.placement(request.UnitIDs[index], placed)
		}
	}
	return batch.Result{Decisions: decisions, Placements: placements}, nil
}

// ReasonVideoUnit is why a unit of video files is not identified as an album: it is recorded as video works until a lookup names them.
const ReasonVideoUnit = "video files are recorded unverified, not matched as an album"

// isVideoUnit says every file of the unit is a video (a music library's concert film or clip).
func isVideoUnit(unit inspect.Unit) bool {
	return len(unit.Media) > 0 && !slices.ContainsFunc(unit.Media, func(file inspect.File) bool { return !IsVideoFile(file.RelPath) })
}

// decideVideoUnit names each video of a unit by MusicBrainz; the unit commits when every one is named at the threshold,
// else it waits unmatched and is added unverified. videoIdentities[index] receives what was found.
func (i Importer) decideVideoUnit(
	ctx context.Context, request batch.Request, index int, relPaths []string, probes []mediatool.Probe, videoIdentities [][]*VideoIdentity, threshold float64,
) library.UnitDecision {
	unit := request.Units[index]
	kind := ""
	if i.config.Kinds != nil {
		kind = i.config.Kinds(request.CategoryID)
	}
	byPath := make(map[string]mediatool.Probe, len(probes))
	for at, probe := range probes {
		byPath[relPaths[at]] = probe
	}
	identities := make([]*VideoIdentity, len(unit.Media))
	named := 0
	var confidence float64
	// A download grabbed for one wanted video is that video, whatever its name says; with several files it cannot tell which.
	var grabbedFor *VideoIdentity
	if i.config.WantedVideo != nil && request.AcquisitionID != nil && len(unit.Media) == 1 {
		var err error
		if grabbedFor, err = i.config.WantedVideo(ctx, *request.AcquisitionID); err != nil {
			i.logger.Warn("music: wanted video of a download not read", slog.Int64("acquisition_id", *request.AcquisitionID), slog.Any("error", err))
		}
	}
	for at, file := range unit.Media {
		claim := InspectVideo(file.RelPath, byPath[file.RelPath], kind)
		identity, err := grabbedFor, error(nil)
		if identity == nil {
			identity, err = IdentifyVideo(ctx, i.config.Videos, claim, threshold)
		}
		if err != nil {
			i.logger.Warn("music: video lookup failed", slog.String("path", file.RelPath), slog.Any("error", err))
		}
		identities[at] = identity
		if identity != nil {
			named++
			confidence = identity.Confidence
		}
	}
	videoIdentities[index] = identities
	if named == len(unit.Media) && !request.Preview {
		return library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitCommit, Confidence: confidence}
	}
	return library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: ReasonVideoUnit, Confidence: confidence}
}

// commitVideoUnit adds a video unit as unverified video works; the committer must be a VideoCommitter.
func (i Importer) commitVideoUnit(
	ctx context.Context, request batch.Request, index int, relPaths []string, probes []mediatool.Probe, identities []*VideoIdentity,
) (Placed, error) {
	committer, ok := i.config.Committer.(VideoCommitter)
	if !ok {
		return Placed{}, errors.New("music: the committer places no video")
	}
	unit := request.Units[index]
	byPath := make(map[string]mediatool.Probe, len(probes))
	for at, probe := range probes {
		byPath[relPaths[at]] = probe
	}
	unitProbes := make([]mediatool.Probe, len(unit.Media))
	for at, file := range unit.Media {
		unitProbes[at] = byPath[file.RelPath]
	}
	return committer.CommitVideoUnit(ctx, request.SourcePath, unit, unitProbes, identities, request.CategoryID, request.Ladder)
}

// CommitHeld implements batch.HeldCommitter: the album a person named, or the unit's own top candidate. A unit
// already added as an unverified album has its files moved onto the album instead of placed a second time.
func (i Importer) CommitHeld(ctx context.Context, request batch.HeldRequest) (library.Placement, error) {
	ctx = musicbrainz.ForLibrary(ctx, request.CategoryID)
	best, ok := chosenIdentification(request)
	if !ok {
		return library.Placement{}, batch.ErrUnresolved
	}
	if i.config.Committer == nil {
		return library.Placement{}, fmt.Errorf("music: commit held unit: no committer")
	}
	if unverified, ok := i.config.Committer.(UnverifiedCommitter); ok && request.WorkID != 0 {
		placed, err := unverified.IdentifyUnverified(ctx, request.WorkID, best, request.CategoryID)
		if err != nil {
			return library.Placement{}, err
		}
		// The unit's files were recorded when it was added; only the album they belong to changes.
		return library.Placement{
			WorkID: placed.WorkID, WorkCreated: placed.WorkCreated, EditionID: placed.EditionID, EditionCreated: placed.EditionCreated,
		}, nil
	}
	absolute := make([]string, len(request.Unit.Media))
	for index, f := range request.Unit.Media {
		absolute[index] = path.Join(request.SourcePath, f.RelPath)
	}
	probes, err := i.config.Tool.ReadTags(ctx, absolute)
	if err != nil {
		return library.Placement{}, fmt.Errorf("music: probe held unit: %w", err)
	}
	matched := i.namedAlbumPlacement(ctx, request, probes)
	placed, err := i.config.Committer.CommitMatchedUnit(ctx, request.SourcePath, request.Unit, best,
		UnitTagSourceFor(request.SourcePath, probes), request.CategoryID, matched, nil)
	if err != nil {
		return library.Placement{}, err
	}
	return *i.placement(0, placed), nil
}

// chosenIdentification is what a held unit is placed as: the album a person named outranks any guess.
func chosenIdentification(request batch.HeldRequest) (model.Identification, bool) {
	if request.As != nil && request.Guess == 0 {
		return model.Identification{
			Strategy:   "manual",
			ExternalID: &model.ExternalID{Provider: request.As.Provider, ExternalID: request.As.ExternalID, Confidence: 1},
			Precedence: model.PrecedenceAsserted,
			Confidence: 1,
		}, true
	}
	if request.Top == nil {
		return model.Identification{}, false
	}
	return *request.Top, true
}

// placement is placed in the form the import session records, with its naming notices logged.
func (i Importer) placement(unitID int64, placed Placed) *library.Placement {
	for mediaObjectID, messages := range placed.PathWarnings {
		for _, message := range messages {
			i.logger.Warn("import commit: naming warning",
				slog.Int64("unit_id", unitID), slog.Int64("media_object_id", mediaObjectID), slog.String("warning", message))
		}
	}
	for _, skipped := range placed.ExtrasSkipped {
		i.logger.Warn("import commit: extra file not placed", slog.Int64("unit_id", unitID), slog.String("reason", skipped))
	}
	extras := make([]library.UnitExtra, len(placed.Extras))
	for index, relPath := range placed.Extras {
		extras[index] = library.UnitExtra{RootFolderID: placed.RootFolderID, RelPath: relPath}
	}
	return &library.Placement{
		WorkID: placed.WorkID, WorkCreated: placed.WorkCreated, EditionID: placed.EditionID, EditionCreated: placed.EditionCreated,
		MediaObjectIDs: placed.MediaObjectIDs, LibraryPathIDs: placed.LibraryPathIDs,
		TransferMethods: placed.TransferMethodNames(), Extras: extras,
	}
}

// identifiers are IDENTIFY's rungs for one batch: embedded ids, then a search, then the audio's fingerprint.
func (i Importer) identifiers(sourcePath string, tags Resolved) []model.Identifier {
	identifiers := []model.Identifier{NewMBIDIdentifier(tags)}
	if i.config.Search != nil {
		identifiers = append(identifiers, NewSearchIdentifier(SearchIdentifierConfig{Tags: tags, Search: i.config.Search}))
	}
	// No Enabled() check: without a key the identifier answers nothing, so one place decides whether the rung is on (D12).
	if i.config.AcoustID != nil {
		identifiers = append(identifiers, NewAcoustIDIdentifier(AcoustIDIdentifierConfig{
			Fingerprints: i.config.Tool, Lookup: i.config.AcoustID,
			Absolute: func(relPath string) string { return path.Join(sourcePath, relPath) },
		}))
	}
	return identifiers
}

// provenanceSeeds are the release groups a download was grabbed for, none for any other import; best-effort.
func (i Importer) provenanceSeeds(ctx context.Context, acquisitionID *int64) []string {
	if i.config.Provenance == nil || acquisitionID == nil {
		return nil
	}
	seeds, err := i.config.Provenance(ctx, *acquisitionID)
	if err != nil {
		i.logger.Warn("import provenance unreadable", slog.Int64("acquisition_id", *acquisitionID), slog.Any("error", err))
	}
	return seeds
}

// refineUnit improves one unit's chain result; it must be safe to call concurrently.
type refineUnit func(ctx context.Context, unit inspect.Unit, result model.ChainResult) (model.ChainResult, *AlbumPlacement)

// albumRefiner matches units as albums over the batch's probes, then by their audio; nil without an album lookup.
// seeds are release groups always scored, whatever the files' names recall.
func (i Importer) albumRefiner(sourcePath string, relPaths []string, probes []mediatool.Probe, threshold float64, seeds []string) refineUnit {
	if i.config.Albums == nil {
		return nil
	}
	naming := AudioNaming{Fingerprints: i.config.Tool, AcoustID: i.config.AcoustID, Albums: i.config.Albums}
	byPath := make(map[string]mediatool.Probe, len(probes))
	for index, probe := range probes {
		byPath[relPaths[index]] = probe
	}
	return func(ctx context.Context, unit inspect.Unit, result model.ChainResult) (model.ChainResult, *AlbumPlacement) {
		refined, placement, err := RefineUnitByAlbum(ctx, i.config.Albums, unit, byPath, result, threshold, seeds)
		if err != nil {
			i.logger.Warn("import album identification failed", slog.String("unit", unit.Directory), slog.Any("error", err))
		}
		// Still nothing: name the files from their audio (D12), as a scan does.
		if refined.Best == nil && placement == nil {
			audio, audioPlacement, err := RefineUnitByAudio(ctx, naming, unit, byPath,
				func(relPath string) string { return path.Join(sourcePath, relPath) }, refined, threshold, seeds)
			if err != nil {
				i.logger.Warn("import audio identification failed", slog.String("unit", unit.Directory), slog.Any("error", err))
			} else {
				refined, placement = audio, audioPlacement
			}
		}
		return refined, placement
	}
}

// identifyUnit runs IDENTIFY for one unit and turns DECIDE's outcome into a decision; an unreadable unit is FAILED.
func identifyUnit(
	ctx context.Context, unitID int64, unit inspect.Unit, identifiers []model.Identifier, cfg model.ChainConfig, refine refineUnit,
) (library.UnitDecision, model.ChainResult, *AlbumPlacement) {
	result, err := IdentifyUnit(ctx, unit, identifiers, cfg)
	var placement *AlbumPlacement
	if err == nil && refine != nil {
		result, placement = refine(ctx, unit, result)
	}
	decision := model.DecisionFailed
	var confidence float64
	var candidatesJSON string
	if err == nil {
		decision = model.Decide(result)
		if result.Best != nil {
			confidence = result.Best.Confidence
		}
		if len(result.Candidates) > 0 {
			if data, marshalErr := json.Marshal(result.Candidates); marshalErr == nil {
				candidatesJSON = string(data)
			}
		}
	}
	return library.UnitDecision{UnitID: unitID, Decision: string(decision), Confidence: confidence, CandidatesJSON: candidatesJSON},
		result, placement
}

// holdForReview is a preview's own override of DECIDE: what would have committed waits for a person. Album matching
// answers on its own capped scale, so raising the threshold could not do the same; SKIP and FAILED are left alone.
func holdForReview(decisions []library.UnitDecision) {
	for index, decision := range decisions {
		if decision.Decision == library.UnitCommit {
			decisions[index].Decision = library.UnitQueue
			decisions[index].Reason = batch.ReasonPreview
		}
	}
}

// namedAlbumPlacement places a unit a person named as an album under that album, not under what its files and folder
// say: tracks matched to its tracklist when they fit, else just its title and artist. Nil when nothing is named.
func (i Importer) namedAlbumPlacement(ctx context.Context, request batch.HeldRequest, probes []mediatool.Probe) *AlbumPlacement {
	if i.config.Albums == nil || request.As == nil || request.Guess != 0 || request.As.Provider != ProviderReleaseGroup {
		return nil
	}
	var locals []LocalTrack
	var relPaths []string
	for index, media := range request.Unit.Media {
		if index < len(probes) && probes[index].Err == nil {
			locals = append(locals, ReadLocalTrack(probes[index].Tags, probes[index].Measured, media.RelPath))
			relPaths = append(relPaths, media.RelPath)
		}
	}
	decision, err := IdentifyAlbum(ctx, i.config.Albums, musicbrainz.PriorityIdentify, locals, Seeds{Grabbed: []string{request.As.ExternalID}})
	if err == nil && decision.Best != nil && decision.Best.Candidate.ReleaseGroup.MBID == request.As.ExternalID {
		return PlacementOf(decision, relPaths)
	}
	group, _, err := i.config.Albums.Album(ctx, musicbrainz.PriorityIdentify, request.As.ExternalID, "")
	if err != nil {
		i.logger.Warn("named album could not be looked up", slog.String("album", request.As.ExternalID), slog.Any("error", err))
		return nil
	}
	return &AlbumPlacement{AlbumTitle: group.Title, ArtistName: creditNames(group.ArtistCredit)}
}

// claimless says the import is of a download whose release named no format and a profile gate is set.
func (i Importer) claimless(ctx context.Context, request batch.Request) bool {
	gate := i.config.Profile
	return gate != nil && gate.Claimless != nil && gate.Accepts != nil && request.AcquisitionID != nil && request.CategoryID != 0 &&
		gate.Claimless(ctx, *request.AcquisitionID)
}

// probedFilesFit is whether every audio file of the unit, as probed, passes a profile of the library; a file nothing can
// judge passes, and a file that could not be probed is left to the placing.
func (i Importer) probedFilesFit(request batch.Request, unit inspect.Unit, relPaths []string, probes []mediatool.Probe) bool {
	at := make(map[string]int, len(relPaths))
	for index, rel := range relPaths {
		at[rel] = index
	}
	for _, file := range unit.Media {
		index, found := at[file.RelPath]
		if !found || index >= len(probes) || probes[index].Err != nil || probes[index].Measured.AudioCodec == "" {
			continue
		}
		measured := probes[index].Measured
		facts := score.Facts{File: score.FileFacts{
			AudioCodec: measured.AudioCodec, Extension: strings.TrimPrefix(strings.ToLower(path.Ext(file.RelPath)), "."),
			Lossless: mediatool.LosslessCodec(measured.AudioCodec), AudioBitrate: measured.AudioBitrate, OverallBitrate: measured.OverallBitrate,
		}}
		if accepts, known := i.config.Profile.Accepts(request.CategoryID, facts); known && !accepts {
			i.logger.Info("import: a probed file meets no profile of the library", slog.String("file", file.RelPath), slog.String("codec", measured.AudioCodec))
			return false
		}
	}
	return true
}
