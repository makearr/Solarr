package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func linkWithEdition(group string, mediaObjectID int64) library.WorkLink {
	return library.WorkLink{
		Provider: "musicbrainz:release-group", ExternalID: group, Confidence: 1, MediaObjectID: mediaObjectID,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Title " + group, SortTitle: "title " + group},
		Edition: &library.EditionLink{
			Provider: "musicbrainz:release", ExternalID: "release-1", Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "release-1"},
		},
	}
}

func editionOwner(t *testing.T, opened *store.Store, editionID int64) int64 {
	t.Helper()
	var owner int64
	if err := opened.Read().QueryRowContext(t.Context(), `SELECT work_id FROM edition WHERE id = ?`, editionID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

// IDENT-1: an edition a provider now places under another work follows it, unless files hang on it and the link brings none.
func TestResolveEdition_MovesAnEditionNoFileHangsOnAndKeepsOneThatHasFiles(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	first, err := identity.ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{linkWithEdition("group-a", 0)})
	if err != nil {
		t.Fatal(err)
	}
	editionID := *first[0].EditionID

	second, err := identity.ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{linkWithEdition("group-b", 0)})
	if err != nil {
		t.Fatal(err)
	}
	if *second[0].EditionID != editionID || editionOwner(t, opened, editionID) != second[0].WorkID {
		t.Fatalf("edition %d belongs to work %d, want the second work %d", editionID, editionOwner(t, opened, editionID), second[0].WorkID)
	}

	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{{RelPath: "a/1.flac", SizeBytes: 1, QuickHash: "h", FirstSeenAt: firstSeen}})
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		item, err := queries.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: second[0].WorkID, EditionID: sql.NullInt64{Int64: editionID, Valid: true}, CategoryID: 1, ProfileID: 1, Monitored: 1, Dirty: 1,
			SatisfactionKind: "SET", State: "MISSING",
		})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO desired_item_file (desired_item_id, media_object_id, role, media_ref, added_at) VALUES (?, ?, 'primary', '', 1)`,
			item.ID, registered[0].MediaObjectID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	third, err := identity.ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{linkWithEdition("group-c", 0)})
	if err != nil {
		t.Fatal(err)
	}
	if editionOwner(t, opened, editionID) != second[0].WorkID || third[0].WorkID == second[0].WorkID {
		t.Errorf("an edition with files moved to work %d", editionOwner(t, opened, editionID))
	}
	fourth, err := identity.ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{linkWithEdition("group-c", registered[0].MediaObjectID)})
	if err != nil {
		t.Fatal(err)
	}
	if editionOwner(t, opened, editionID) != fourth[0].WorkID {
		t.Errorf("a link carrying a file left the edition under work %d, want %d", editionOwner(t, opened, editionID), fourth[0].WorkID)
	}
}
