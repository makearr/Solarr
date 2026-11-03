package works

import (
	"context"
	"errors"
	"log/slog"

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
	for index, unit := range request.Units {
		decision := library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitQueue, Reason: "no " + i.config.Noun + " of the library was found in it"}
		workID, candidates := seeded, []model.Identification(nil)
		if workID == 0 {
			workID, candidates = i.config.Units.Resolve(ctx, request, unit)
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
