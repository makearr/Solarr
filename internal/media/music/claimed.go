package music

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// recordClaimedAttributes writes ADR-39's claimed half once per distinct release folder, pointing each file's media_object at it; mediaObjectIDs aligns with observed.
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

// attributeClaimFromReleaseName parses a folder name (ADR-18) into library's shape; fields with no music equivalent stay zero so Intern stores NULL.
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

// editionTokens splits Parse's space-joined Edition into tokens; empty yields nil so Intern reads it as not stated, not "[]".
func editionTokens(edition string) []string {
	if edition == "" {
		return nil
	}
	return strings.Fields(edition)
}
