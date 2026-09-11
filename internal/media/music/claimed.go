package music

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/parse"
	"github.com/makearr/MediaNexus/internal/scanning/scan"
)

// recordClaimedAttributes writes ADR-39's claimed half (todo 4.2) for every
// file this batch registered: the release folder name, parsed once per
// distinct folder rather than once per file (a release's tracks all share
// one folder and so all share one claim), interned and pointed at from each
// file's own media_object. mediaObjectIDs is positionally aligned with
// observed, the same contract RegisterBatch's own caller already relies on.
func (c Consumer) recordClaimedAttributes(ctx context.Context, observed []scan.Observed, mediaObjectIDs []int64) error {
	claimsByFolder := make(map[string]library.AttributeClaim)
	for index, one := range observed {
		folder := path.Dir(one.File.RelPath)
		claim, found := claimsByFolder[folder]
		if !found {
			claim = attributeClaimFromReleaseName(path.Base(folder))
			claimsByFolder[folder] = claim
		}
		if err := c.physical.RecordClaimed(ctx, mediaObjectIDs[index], claim); err != nil {
			return fmt.Errorf("record claimed attributes for %s: %w", one.File.RelPath, err)
		}
	}
	return nil
}

// attributeClaimFromReleaseName parses one release folder's own name (ADR-18)
// and translates its music-specific claim (model.Attributes) into library's
// cross-media-type shape. Fields with no music equivalent (Resolution,
// VideoCodec, HDR, ...) are left zero, which Intern stores as NULL rather
// than a false claim of "known and empty".
func attributeClaimFromReleaseName(releaseName string) library.AttributeClaim {
	attributes := parse.Parse(releaseName).Attributes
	return library.AttributeClaim{
		AudioCodec:    attributes.Format,
		Lossless:      attributes.Lossless,
		Container:     attributes.Container,
		Source:        attributes.Source,
		BitDepth:      attributes.BitDepth,
		SampleRate:    attributes.SampleRate,
		EditionTokens: editionTokens(attributes.Edition),
		ReleaseGroup:  attributes.ReleaseGroup,
		ProperRepack:  attributes.ProperRepack,
		BitrateKbps:   attributes.BitrateKbps,
		BitrateMode:   attributes.BitrateMode,
		VBRPreset:     attributes.VBRPreset,
	}
}

// editionTokens splits Parse's own space-joined Edition string ("flac (m4a)
// lossless") into AttributeClaim.EditionTokens' []string shape -- an empty
// Edition yields nil, which Intern's own jsonArray already treats as "none
// stated" rather than the literal string "[]".
func editionTokens(edition string) []string {
	if edition == "" {
		return nil
	}
	return strings.Fields(edition)
}
