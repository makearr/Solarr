// Package batch is the contract between the import job and a media type that identifies and places a walked batch of
// units in its own way (ADR-23).
package batch

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

// ReasonPreview is why a unit a preview run would otherwise have placed waits for a person.
const ReasonPreview = "held for review"

// ReasonNothingNew skips a unit that adds nothing its work lacks; the download brought nothing usable, so it fails and is
// blocklisted (user, 2026-10-24), as Sonarr's "not an upgrade" release.
const ReasonNothingNew = "nothing in it is missing from the library or better than what it holds"

// StrategyResolved is the candidate a unit was resolved to before its library could not take it.
const StrategyResolved = "resolved"

// WaitForRoot is a unit resolved to workID that its library could not take: decided but not placed, so the download is
// kept and the import retry places it once a root can. The work rides in the candidates; a work id means placed.
func WaitForRoot(decision library.UnitDecision, workID int64, err error) library.UnitDecision {
	decision.Decision, decision.Confidence, decision.Reason, decision.WorkID = library.UnitCommit, 1, err.Error(), nil
	// The whole form, not library.CandidatesJSON's compact one, which has no work id.
	resolved, _ := json.Marshal([]model.Identification{{
		Strategy: StrategyResolved, WorkID: &workID, Precedence: model.PrecedenceAsserted, Confidence: 1,
	}})
	decision.CandidatesJSON = string(resolved)
	return decision
}

// VideoSizeFloorBytes keeps a sample or a stub out of a video import: smaller than any real episode or film.
const VideoSizeFloorBytes = 30 << 20

// Request is one walked batch of units a media type identifies and places in its own way.
type Request struct {
	SessionID int64
	// CategoryID is the library to place into; 0 for a session that has none yet, which is identified and placed nowhere.
	CategoryID int64
	// AcquisitionID is the download this import brings in, nil for a folder a person pointed at.
	AcquisitionID *int64
	SourcePath    string
	Units         []inspect.Unit
	// UnitIDs is parallel to Units: the import_unit rows already recorded for them.
	UnitIDs []int64
	// Preview holds every unit that would commit for a person instead.
	Preview bool
	// Ladder overrides the configured transfer methods when set.
	Ladder []string
	// Threshold is the session's confidence a unit must reach to be placed without a person.
	Threshold float64
	// AddUnidentified asks for what nothing identified to be added as unverified, when the type can (D6).
	AddUnidentified bool
}

// Result is what a media type decided and placed for a batch; both are parallel to Request.Units.
type Result struct {
	Decisions []library.UnitDecision
	// Placements holds an entry for each unit that was placed, nil for the rest.
	Placements []*library.Placement
}

// Importer identifies and places a batch of units for one media type, so the import job knows nothing of what a
// season or an album is (ADR-23).
type Importer interface {
	ImportBatch(ctx context.Context, request Request) (Result, error)
}

// AcquisitionSource finds the desired item a finished download was grabbed for.
type AcquisitionSource interface {
	Get(ctx context.Context, id int64) (library.Acquisition, error)
}

// Named is the work a person said a unit is, by a provider's id.
type Named struct{ Provider, ExternalID string }

// HeldRequest is one unit a session held for a person, to be placed by hand.
type HeldRequest struct {
	SessionID  int64
	CategoryID int64
	// AcquisitionID is the download the unit came in, nil for a folder a person pointed at.
	AcquisitionID *int64
	SourcePath    string
	Unit          inspect.Unit
	// WorkID is what the import resolved the unit to, 0 when it found nothing.
	WorkID int64
	// As is the work a person named instead; nil places the unit as resolved.
	As *Named
	// Guess is As's confidence when As is the import's own best guess ("Commit anyway"), not a person's; 0 otherwise.
	Guess float64
	// Top is the unit's own top-ranked identification, nil when it has none.
	Top *model.Identification
}

// ErrGuessTooWeak is "Commit anyway" on a guess the media type will not place without a person naming the work.
var ErrGuessTooWeak = errors.New("batch: the best guess is too weak to place; say what it is with Identify as")

// ErrUnresolved is a held unit that resolved to no work and was given none.
var ErrUnresolved = errors.New("batch: the unit was matched to nothing; say what it is")

// ErrNothingToPlace is a held unit none of whose files adds anything to the work.
var ErrNothingToPlace = errors.New("batch: none of the unit's files is something the library does not already hold")

// SizeFloor is an Importer whose walk leaves out media files smaller than MediaSizeFloorBytes.
type SizeFloor interface {
	MediaSizeFloorBytes() int64
}

// HeldCommitter is an Importer whose held units a person commits by hand: as resolved, or as a work named.
type HeldCommitter interface {
	CommitHeld(ctx context.Context, request HeldRequest) (library.Placement, error)
}
