package music

import (
	"context"
	"fmt"
	"path"

	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// IdentifyUnit runs the ADR-24 chain over one inspect.Unit as a whole (ADR-48's
// IDENTIFY step: "the ADR-24 chain, per unit, independently") via
// model.RunChainForFiles rather than per file and merged afterward -- a cheap
// strategy that resolves confidently from any one file in the unit skips a more
// expensive strategy for the rest of it, the same early-stop RunChain already gives
// a single scanned file. identifiers must already be built over a TagSource that
// resolves this unit's own import root (UnitTagSourceFor); this function only turns
// the unit's media files into the model.FileRef the chain expects.
//
// An import unit has no MediaObjectID or RootFolderID yet (ADR-48) -- exactly what
// the widened model.FileRef exists for.
func IdentifyUnit(
	ctx context.Context, unit inspect.Unit, identifiers []model.Identifier, cfg model.ChainConfig,
) (model.ChainResult, error) {
	if len(unit.Media) == 0 {
		return model.ChainResult{}, nil
	}

	files := make([]model.FileRef, len(unit.Media))
	for index, media := range unit.Media {
		files[index] = model.FileRef{RelPath: media.RelPath}
	}

	result, err := model.RunChainForFiles(ctx, identifiers, files, cfg)
	if err != nil {
		return model.ChainResult{}, fmt.Errorf("music: identify unit %q: %w", unit.Directory, err)
	}
	return result, nil
}

// UnitTagSourceFor answers the chain's tag lookups for an import-inspection unit
// from a batch already probed by mediatool -- ADR-28/ADR-30's "one probe per file,
// not per identifier" applied to an import root the same way tagSourceFor applies it
// to a scanned one. root is the path inspect.Run was given; a FileRef's RelPath is
// relative to it either way, so this only differs from tagSourceFor in not closing
// over a settings.RootFolder that an import unit does not have.
func UnitTagSourceFor(root string, probes []mediatool.Probe) Resolved {
	return Resolved{
		Tags: NewBatchTags(probes),
		Path: func(file model.FileRef) (string, bool) { return path.Join(root, file.RelPath), true },
	}
}
