package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// The rollup is rebuilt each time, not added to, and a library reads only its own figures.
func TestStatistics_ComputeRebuildsAndReadsPerLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	work := createWork(t, opened, "music", "Album")
	createDesired(t, opened, work.ID, "SET", 10)
	other := createWork(t, opened, "music", "Elsewhere")
	createDesiredInCategory(t, opened, other.ID, 2)

	for range 2 {
		if err := statistics.Compute(t.Context(), map[int64]int64{}, 5_000); err != nil {
			t.Fatalf("Compute: %v", err)
		}
	}
	got, err := statistics.ForCategory(t.Context(), library.StatAlbumsByState, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "MISSING" || got[0].Count != 1 || got[0].ComputedAt != 5_000 {
		t.Fatalf("library 1 albums = %+v, want one MISSING computed at 5000, not doubled by the second run", got)
	}
	if other, _ := statistics.ForCategory(t.Context(), library.StatAlbumsByState, 3); len(other) != 0 {
		t.Errorf("a library with nothing got %+v", other)
	}
}

// An artist with an album held is compared against their discography; only those with albums missing are listed,
// most missing first (revision 6.4.5).
func TestStatistics_IncompleteDiscographiesAreListedMostMissingFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	people := library.NewPeople(opened)
	physical := library.NewPhysical(opened)

	var held []library.ArtistHeld
	for index, name := range []string{"Few Missing", "Many Missing", "Complete"} {
		person := resolvePersonNamed(t, people, "mbid-"+name, name)
		work := createWork(t, opened, "music", name+" Album")
		createDesired(t, opened, work.ID, "SET", 10)
		if err := people.AddCredit(t.Context(), work.ID, person, "artist", 0); err != nil {
			t.Fatal(err)
		}
		registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
			{RelPath: name + "/01.flac", SizeBytes: 1, QuickHash: "h" + name, FirstSeenAt: time.Unix(1, 0)},
		})
		if err != nil {
			t.Fatal(err)
		}
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			if err := q.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: work.ID, MediaObjectID: registered[0].MediaObjectID, FirstSeenAt: 1}); err != nil {
				return err
			}
			return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
				Provider: "musicbrainz:release-group", ExternalID: "rg-" + name, WorkID: work.ID, Confidence: 1, FirstSeenAt: 1,
			})
		})
		_ = index
	}
	artists, err := statistics.ArtistsHeld(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	held = artists
	if len(held) != 3 || held[0].ArtistMBID != "mbid-Few Missing" || len(held[0].ReleaseGroups) != 1 || held[0].ReleaseGroups[0] != "rg-Few Missing" {
		t.Fatalf("artists held = %+v, want three, each holding its one release group", held)
	}

	rows := []library.DiscographyCoverage{
		{PersonID: held[0].PersonID, Held: 1, Total: 2},
		{PersonID: held[1].PersonID, Held: 1, Total: 6},
		{PersonID: held[2].PersonID, Held: 4, Total: 4},
	}
	if err := statistics.SetDiscographyCoverage(t.Context(), 1, rows, 9_000); err != nil {
		t.Fatal(err)
	}
	listed, err := statistics.IncompleteDiscographies(t.Context(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].PersonID != held[1].PersonID || listed[1].PersonID != held[0].PersonID {
		t.Fatalf("incomplete = %+v, want the six-album artist then the two-album one, the complete one left out", listed)
	}
}
