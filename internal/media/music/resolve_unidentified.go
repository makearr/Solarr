package music

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrUnsupportedProvider reports a resolve request naming a provider this
// package has no lookup for. Only ProviderReleaseGroup is wired today, since
// that is the only provider the ADR-24 chain's own candidates ever carry into
// the queue (identifier.go, search_identifier.go).
var ErrUnsupportedProvider = fmt.Errorf("music: unsupported provider for manual resolve")

// ResolveUnidentified is section 21.3's manual-resolve action: given a
// candidate a human picked (either one of RunChain's own ranked suggestions,
// or, in future, a live search result), fetch its real title from the
// provider, resolve-or-create the work, link the file's own root folder, and
// clear the queue entry. Confidence is 1.0 -- a human explicitly chose this,
// which is stronger evidence than anything the chain could have inferred on
// its own.
//
// This reaches only as far as SearchIdentifier's own automatic resolution
// does: the file's work becomes real and browsable, but no edition or
// tracklist alignment is attempted (the same "browsable, not yet enrichable"
// limit search_identifier.go's own doc comment already names).
func ResolveUnidentified(
	ctx context.Context, cl clock.Clock, provider musicbrainz.Provider,
	identity library.Identity, unidentified library.Unidentified,
	mediaObjectID int64, extProvider, externalID string,
) (workID int64, err error) {
	if extProvider != ProviderReleaseGroup {
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedProvider, extProvider)
	}

	entry, err := unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: %w", err)
	}

	releaseGroup, _, err := provider.Album(ctx, musicbrainz.PriorityInteractive, externalID, "")
	if err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: musicbrainz: %w", err)
	}

	workIDs, err := identity.ResolveWorks(ctx, cl.Now(), []library.WorkLink{{
		Provider: extProvider, ExternalID: externalID, Confidence: 1.0,
		Work: library.WorkIdentity{
			MediaType: "music", CanonicalTitle: releaseGroup.Title, SortTitle: SortTitle(releaseGroup.Title),
			PrimaryType: releaseGroup.PrimaryType,
		},
		RootFolderID: entry.RootFolderID, MediaObjectID: mediaObjectID,
	}})
	if err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: %w", err)
	}
	workID = workIDs[0]

	if err := unidentified.Clear(ctx, []int64{mediaObjectID}); err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: clear queue entry: %w", err)
	}
	return workID, nil
}
