package library_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// M1f: a work whose MusicBrainz id was merged follows the new id, unless another work holds it.
func TestFollowMergedIDs_MovesToTheNewIDUnlessAnotherWorkHoldsIt(t *testing.T) {
	opened, queries := newLibrary(t)
	merged := createWork(t, opened, "music", "Homework")
	holder := createWork(t, opened, "music", "Homework (deluxe)")
	for workID, externalID := range map[int64]string{merged.ID: "rg-old", holder.ID: "rg-held"} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
				Provider: "musicbrainz:release-group", ExternalID: externalID, WorkID: workID, Confidence: 1, FirstSeenAt: 1,
			})
		})
	}
	identity := library.NewIdentity(opened)

	moved, _, err := identity.FollowMergedIDs(t.Context(), library.MergedIDs{
		WorkID: merged.ID, WorkProvider: "musicbrainz:release-group", OldWorkID: "rg-old", NewWorkID: "rg-held",
	})
	if err != nil || moved {
		t.Fatalf("onto a held id: moved %v, %v; want left alone", moved, err)
	}
	moved, _, err = identity.FollowMergedIDs(t.Context(), library.MergedIDs{
		WorkID: merged.ID, WorkProvider: "musicbrainz:release-group", OldWorkID: "rg-old", NewWorkID: "rg-new",
	})
	if err != nil || !moved {
		t.Fatalf("onto a free id: moved %v, %v; want moved", moved, err)
	}
	found, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-new",
	})
	if err != nil || found.ID != merged.ID {
		t.Errorf("rg-new resolves to %d, %v; want work %d", found.ID, err, merged.ID)
	}
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-old",
	}); err == nil {
		t.Error("rg-old still resolves after the move")
	}
}
