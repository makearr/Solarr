package music

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

// refinementFor turns one MusicBrainz answer into what library.RefineEdition
// needs: interpreting MusicBrainz's own vocabulary into MediaNexus's is this
// package's job, never core's (ADR-23).
func refinementFor(rg musicbrainz.ReleaseGroup, release musicbrainz.Release) (
	workYear *int64, refinement library.EditionRefinement, parts []library.PartIdentity,
) {
	workYear = parseYear(rg.FirstReleaseDate)

	refinement = library.EditionRefinement{
		// Status is the closest thing a Release has to a "kind of edition"
		// (official/promotion/bootleg/pseudo-release) -- label stays the release
		// MBID permanently (identity.go's resolveEdition comment), so changing
		// kind alone can never collide with UNIQUE(work_id, kind, label).
		Kind:            orUnknownStatus(release.Status),
		Year:            parseYear(release.Date),
		Format:          formatOf(release.Media),
		CatalogueNumber: release.CatalogueNumber,
		Barcode:         release.Barcode,
		RuntimeMs:       totalRuntimeMs(release.Media),
	}

	for _, medium := range release.Media {
		for _, track := range medium.Tracks {
			parts = append(parts, partFor(medium, track))
		}
	}
	return workYear, refinement, parts
}

// orUnknownStatus never writes an empty kind back -- an edition already refined
// once must not regress to looking unenriched.
func orUnknownStatus(status string) string {
	if status == "" {
		return "unknown"
	}
	return status
}

// formatOf reports the first medium's format. Music editions in this library are
// overwhelmingly single-format (a release is not "half CD, half vinyl"); a genuine
// mixed-format box set is rare enough that recording only the first is an accepted
// simplification, not a design position.
func formatOf(media []musicbrainz.Medium) string {
	if len(media) == 0 {
		return ""
	}
	return media[0].Format
}

func totalRuntimeMs(media []musicbrainz.Medium) *int64 {
	var total int64
	var found bool
	for _, medium := range media {
		for _, track := range medium.Tracks {
			if track.DurationMs > 0 {
				total += track.DurationMs
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return &total
}

func partFor(medium musicbrainz.Medium, track musicbrainz.Track) library.PartIdentity {
	part := library.PartIdentity{
		MediaRef: mediaRef(medium.Position, track.Position),
		SortKey:  sortKey(medium.Position, track.Position),
		Title:    track.Title,
	}
	if track.DurationMs > 0 {
		duration := track.DurationMs
		part.RuntimeMs = &duration
	}
	if track.RecordingMBID != "" {
		part.ExternalID = &library.PartExternalID{
			Provider: ProviderRecording, ExternalID: track.RecordingMBID, Confidence: 1.0,
		}
	}
	return part
}

// mediaRef follows the "disc1:track5" convention documentation/notes already
// establish for this package.
func mediaRef(discPosition, trackPosition int) string {
	return fmt.Sprintf("disc%d:track%d", discPosition, trackPosition)
}

// sortKey is module-supplied and zero-padded (work_part.sort_key's own doc
// comment): core never parses media_ref to order parts, so the ordering has to
// live here instead.
func sortKey(discPosition, trackPosition int) string {
	return fmt.Sprintf("%04d%04d", discPosition, trackPosition)
}

// parseYear reads MusicBrainz's partial-date convention (YYYY, YYYY-MM or
// YYYY-MM-DD) and keeps only the year. An unparseable or empty date answers nil,
// never zero -- zero would read as a real year to UpdateWorkYearIfNull.
func parseYear(date string) *int64 {
	year, _, _ := strings.Cut(date, "-")
	if len(year) != 4 {
		return nil
	}
	parsed, err := strconv.ParseInt(year, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
