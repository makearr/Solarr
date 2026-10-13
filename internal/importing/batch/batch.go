// Package batch is the contract between the import job and a media type that identifies and places a walked batch of
// units in its own way (ADR-23).
package batch

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

// Request is one walked batch of units a media type identifies and places in its own way.
type Request struct {
	SessionID  int64
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

// Named is the work a person said a unit is, by a provider's id.
type Named struct{ Provider, ExternalID string }

// HeldRequest is one unit a session held for a person, to be placed by hand.
type HeldRequest struct {
	SessionID  int64
	CategoryID int64
	SourcePath string
	Unit       inspect.Unit
	// WorkID is what the import resolved the unit to, 0 when it found nothing.
	WorkID int64
	// As is the work a person named instead; nil places the unit as resolved.
	As *Named
}

// ErrUnresolved is a held unit that resolved to no work and was given none.
var ErrUnresolved = errors.New("batch: the unit was matched to nothing; say what it is")

// ErrNothingToPlace is a held unit none of whose files adds anything to the work.
var ErrNothingToPlace = errors.New("batch: none of the unit's files is something the library does not already hold")

// HeldCommitter is an Importer whose held units a person commits by hand: as resolved, or as a work named.
type HeldCommitter interface {
	CommitHeld(ctx context.Context, request HeldRequest) (library.Placement, error)
}
