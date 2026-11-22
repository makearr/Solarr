package music

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrUnsupportedProvider is only ProviderReleaseGroup is wired, the only provider chain candidates carry into the queue.
var ErrUnsupportedProvider = fmt.Errorf("music: unsupported provider for manual resolve")

// ResolveUnidentified is section 21.3's manual resolve: fetch the picked candidate's title, resolve or create the work, link the root and clear the queue entry, at confidence 1.0 (a human chose); like SearchIdentifier it attempts no edition or tracklist alignment.
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
			MediaType: "music", CanonicalTitle: releaseGroup.Title, SortTitle: library.SortTitle(releaseGroup.Title),
			Subtype: releaseGroup.PrimaryType,
		},
		RootFolderID: entry.RootFolderID, MediaObjectID: mediaObjectID,
	}})
	if err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: %w", err)
	}
	workID = workIDs[0]
	if _, err := identity.LeaveUnverified(ctx, []int64{mediaObjectID}); err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: %w", err)
	}
	if err := unidentified.RecordManual(ctx, mediaObjectID, workID, cl.Now().Unix()); err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: %w", err)
	}

	if err := unidentified.Clear(ctx, []int64{mediaObjectID}); err != nil {
		return 0, fmt.Errorf("music: resolve unidentified: clear queue entry: %w", err)
	}
	return workID, nil
}
