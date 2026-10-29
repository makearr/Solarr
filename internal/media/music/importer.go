package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
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
	Tool      mediatool.Tool
	Search    Searcher
	Albums    AlbumLookup
	AcoustID  AcoustIDLookup
	Committer UnitCommitter
	// Provenance answers the release groups a download was grabbed for, which album identification always scores.
	Provenance func(ctx context.Context, acquisitionID int64) ([]string, error)
	Logger     *slog.Logger
}

// Importer is Lidarr's import behind the import job's batch contract: identify each unit from its tags, its album
// and its audio, decide, and place what clears the session's threshold.
type Importer struct {
	config ImporterConfig
	logger *slog.Logger
}

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
)

// NewImporter builds one.
func NewImporter(config ImporterConfig) Importer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Importer{config: config, logger: logger}
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
	probes, err := i.config.Tool.ReadTags(ctx, absolute)
	if err != nil {
		return batch.Result{}, fmt.Errorf("music: probe batch: %w", err)
	}
	tags := UnitTagSourceFor(request.SourcePath, probes)
	cfg := model.ChainConfig{AcceptThreshold: DefaultAcceptThreshold, AmbiguityThreshold: request.Threshold}
	refine := i.albumRefiner(request.SourcePath, relPaths, probes, request.Threshold, i.provenanceSeeds(ctx, request.AcquisitionID))

	decisions := make([]library.UnitDecision, len(request.Units))
	results := make([]model.ChainResult, len(request.Units))
	matched := make([]*AlbumPlacement, len(request.Units))
	identifiers := i.identifiers(request.SourcePath, tags)
	identified := parallel.Do(ctx, len(request.Units), identifyParallelism, func(ctx context.Context, index int) error {
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

	placements := make([]*library.Placement, len(request.Units))
	if request.CategoryID == 0 || i.config.Committer == nil {
		return batch.Result{Decisions: decisions, Placements: placements}, nil
	}
	for index, decision := range decisions {
		if decision.Decision != library.UnitCommit || results[index].Best == nil {
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
			placed, err := unverified.CommitUnverifiedUnit(ctx, request.SourcePath, request.Units[index], tags, request.CategoryID, request.Ladder)
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
	// No album placement: what album matching decided during IDENTIFY is no longer in memory.
	placed, err := i.config.Committer.CommitMatchedUnit(ctx, request.SourcePath, request.Unit, best,
		UnitTagSourceFor(request.SourcePath, probes), request.CategoryID, nil, nil)
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
