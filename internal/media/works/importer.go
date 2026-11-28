package works

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
)

// UnitPlacer is a media type's part of an import that places one work per download: which work a unit holds, which
// one a person named, and how its files are placed.
type UnitPlacer interface {
	// Resolve finds the work a unit holds; 0 with the candidates it might be when it is not sure.
	Resolve(ctx context.Context, request batch.Request, unit inspect.Unit) (workID int64, candidates []model.Identification)
	// ResolveByName is Resolve from the unit's folder and file names alone: no file opened, no tag, nfo or fingerprint read.
	ResolveByName(ctx context.Context, request batch.Request, unit inspect.Unit) (workID int64, candidates []model.Identification)
	// ResolveNamed is the work a person named for a held unit, 0 when none of the library is; an error refuses the name.
	ResolveNamed(ctx context.Context, categoryID int64, named batch.Named) (int64, error)
	// Place places the unit's files as the work's: batch.ErrNothingToPlace when none adds anything, a
	// *batch.RejectedError when the import rules refuse them.
	Place(ctx context.Context, request batch.Request, unit inspect.Unit, workID int64, upgrade bool) (*library.Placement, error)
}

// ImporterConfig is what an Importer needs; Logger and AfterPlacing are optional.
type ImporterConfig struct {
	// MediaType prefixes what is logged.
	MediaType string
	// Noun is what the type calls its work in the Review queue's reason ("no film of the library was found in it").
	Noun         string
	GuessFloor   float64
	Units        UnitPlacer
	Acquisitions batch.AcquisitionSource
	Desired      library.Desired
	Logger       *slog.Logger
	// AfterPlacing is told of each work an import placed files of.
	AfterPlacing func(ctx context.Context, categoryID, workID int64)
}

// Importer is the batch importer of movies, adult scenes, books and audiobooks: a download holds one work, found from
// what it was grabbed for or else from its names. What it cannot place waits in the Review queue.
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

var (
	_ batch.Importer      = Importer{}
	_ batch.HeldCommitter = Importer{}
)

// NameMatches implements batch.Importer: a download holds one work, so the names stage is the placer's name-only resolve.
func (i Importer) NameMatches(ctx context.Context, request batch.Request) []batch.NameMatch {
	matches := make([]batch.NameMatch, len(request.Units))
	batch.ResolveAll(ctx, len(request.Units), func(ctx context.Context, index int) {
		workID, candidates := i.config.Units.ResolveByName(ctx, request, request.Units[index])
		if workID > 0 {
			matches[index] = batch.NameMatch{WorkID: workID, Confidence: 1, Candidates: candidates}
		}
	})
	return matches
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
		seeded, upgrade = i.seeded(ctx, *request.AcquisitionID)
	}
	type resolution struct {
		workID     int64
		candidates []model.Identification
	}
	resolved := make([]resolution, len(request.Units))
	if seeded == 0 {
		batch.ResolveAll(ctx, len(request.Units), func(ctx context.Context, index int) {
			resolved[index].workID, resolved[index].candidates = i.config.Units.Resolve(ctx, request, request.Units[index])
		})
	}
	// Every unit of a tracked download is the seeded work, so the first one placed wins: the largest goes first.
	for _, index := range visitOrder(request.Units, seeded != 0) {
		unit := request.Units[index]
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no " + i.config.Noun + " of the library was found in it"}
		workID, candidates := seeded, []model.Identification(nil)
		if workID == 0 {
			workID, candidates = resolved[index].workID, resolved[index].candidates
		}
		if workID <= 0 {
			result.Decisions[index] = decision
			result.Decisions[index].CandidatesJSON = library.CandidatesJSON(candidates)
			continue
		}
		decision.WorkID = &workID
		if seeded == 0 && request.NameOf(index).Disagrees(workID, nil) {
			decision.Reason = batch.ReasonNameDisagrees
			result.Decisions[index] = decision
			continue
		}
		if request.Preview {
			decision.Reason, decision.Confidence = batch.ReasonPreview, 1
			result.Decisions[index] = decision
			continue
		}
		placed, err := i.config.Units.Place(ctx, request, unit, workID, upgrade && workID == seeded)
		var refused *batch.RejectedError
		switch {
		case errors.Is(err, batch.ErrNothingToPlace):
			decision.Decision, decision.Reason, decision.WorkID = library.UnitSkip, batch.ReasonNothingNew, nil
			result.Decisions[index] = decision
		case errors.As(err, &refused):
			decision.Reason = refused.Error()
			result.Decisions[index] = decision
		case errors.Is(err, placer.ErrNoRoot):
			result.Decisions[index] = batch.WaitToPlace(decision, workID, err)
		case err != nil:
			i.logger.Warn(i.config.MediaType+" import: could not place unit", slog.String("unit", unit.Directory), slog.Any("error", err))
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

// visitOrder is the order units are tried in: request order, or with several units of one seeded work the one holding
// the largest media file first, so an extra beside the film is not placed as the film. Decisions keep request order.
func visitOrder(units []inspect.Unit, seeded bool) []int {
	order := make([]int, len(units))
	for index := range order {
		order[index] = index
	}
	if seeded && len(units) > 1 {
		slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(largestMedia(units[b]), largestMedia(units[a])) })
	}
	return order
}

func largestMedia(unit inspect.Unit) int64 {
	var largest int64
	for _, file := range unit.Media {
		largest = max(largest, file.SizeBytes)
	}
	return largest
}

// seeded is the work a download was grabbed for; upgrade says it was below its cutoff, so the download may add a
// better version of what is held.
func (i Importer) seeded(ctx context.Context, acquisitionID int64) (workID int64, upgrade bool) {
	acquisition, err := i.config.Acquisitions.Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return 0, false
	}
	item, err := i.config.Desired.Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return 0, false
	}
	return item.WorkID, item.State == StateUpgradable
}

// CommitHeld implements batch.HeldCommitter: the unit goes to the work named, else the one it resolved to.
func (i Importer) CommitHeld(ctx context.Context, request batch.HeldRequest) (library.Placement, error) {
	workID := request.WorkID
	if request.As != nil && request.Guess > 0 && request.Guess < i.config.GuessFloor {
		return library.Placement{}, batch.ErrGuessTooWeak
	}
	if request.As != nil {
		named, err := i.config.Units.ResolveNamed(ctx, request.CategoryID, *request.As)
		if err != nil {
			return library.Placement{}, err
		}
		workID = named
	}
	if workID <= 0 {
		return library.Placement{}, batch.ErrUnresolved
	}
	var upgrade bool
	if request.AcquisitionID != nil {
		seeded, upgradable := i.seeded(ctx, *request.AcquisitionID)
		upgrade = upgradable && workID == seeded
	}
	placed, err := i.config.Units.Place(ctx, batch.Request{SessionID: request.SessionID, CategoryID: request.CategoryID, SourcePath: request.SourcePath}, request.Unit, workID, upgrade)
	if err != nil {
		return library.Placement{}, err
	}
	if i.config.AfterPlacing != nil {
		i.config.AfterPlacing(ctx, request.CategoryID, workID)
	}
	return *placed, nil
}
