package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func createPart(t *testing.T, opened *store.Store, workID int64, mediaRef, sortKey, title string) librarydb.WorkPart {
	t.Helper()
	var part librarydb.WorkPart
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID:   workID,
			MediaRef: mediaRef,
			SortKey:  sortKey,
			Title:    sql.NullString{String: title, Valid: title != ""},
			// Explicit: an insert overrides the column default, and a part nobody
			// monitors is not what a freshly imported tracklist means.
			Monitored: 1,
		})
		part = row
		return err
	})
	return part
}

// The sort keys run opposite to the references, so a listing that happened to
// order by media_ref would return exactly the wrong answer.
func TestPartsOrderByTheirSortKeyNotTheirReference(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")

	createPart(t, opened, work.ID, "disc1:track09", "0003", "Mercy")
	createPart(t, opened, work.ID, "disc1:track01", "0009", "The Furab Keke")
	createPart(t, opened, work.ID, "disc1:track05", "0006", "Deanna")

	parts, err := queries.ListWorkParts(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("listed %d parts, want 3", len(parts))
	}

	want := []string{"disc1:track09", "disc1:track05", "disc1:track01"}
	for index, part := range parts {
		if part.MediaRef != want[index] {
			t.Errorf("part %d is %q, want %q: ordering followed the reference, not the sort key",
				index, part.MediaRef, want[index])
		}
	}
}

// A recording released on a studio album and again on a compilation is one
// recording and two parts. Finding both is what makes the duplicate answerable.
func TestOneRecordingOnTwoAlbumsResolvesToBothParts(t *testing.T) {
	opened, queries := newLibrary(t)
	album := createWork(t, opened, "music", "Tender Prey")
	compilation := createWork(t, opened, "music", "The Best Of")

	onAlbum := createPart(t, opened, album.ID, "disc1:track01", "0001", "The Furab Keke")
	onCompilation := createPart(t, opened, compilation.ID, "disc1:track04", "0004", "The Furab Keke")

	const recording = "b1a9c0de-0000-4000-8000-000000000001"
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, part := range []int64{onAlbum.ID, onCompilation.ID} {
			err := q.LinkWorkPartExternalID(ctx, librarydb.LinkWorkPartExternalIDParams{
				Provider: "musicbrainz-recording", ExternalID: recording,
				PartID: part, Confidence: 1,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	found, err := queries.FindPartsByExternalID(t.Context(), librarydb.FindPartsByExternalIDParams{
		Provider: "musicbrainz-recording", ExternalID: recording,
	})
	if err != nil {
		t.Fatalf("FindPartsByExternalID: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("the recording resolved to %d parts, want 2: one identifier must reach every "+
			"part carrying it", len(found))
	}

	works := map[int64]bool{}
	for _, part := range found {
		works[part.WorkID] = true
	}
	if !works[album.ID] || !works[compilation.ID] {
		t.Errorf("the recording resolved to works %v, want both %d and %d",
			works, album.ID, compilation.ID)
	}
}

// Two series both number an episode 137. Absolute numbering is only meaningful
// inside its own work, so an unscoped lookup would resolve to whichever came first.
func TestAbsoluteNumberingResolvesWithinItsSeriesOnly(t *testing.T) {
	opened, queries := newLibrary(t)
	first := createWork(t, opened, "tv", "Iho Hiaru")
	second := createWork(t, opened, "tv", "Risube")

	firstPart := createPart(t, opened, first.ID, "s07e12", "0007-0012", "Watch Out")
	secondPart := createPart(t, opened, second.ID, "s04e01", "0004-0001", "Homecoming")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, part := range []int64{firstPart.ID, secondPart.ID} {
			err := q.AddAltNumbering(ctx, librarydb.AddAltNumberingParams{
				PartID: part, Scheme: "absolute", Provider: "anidb", AltRef: "137",
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	for _, entry := range []struct {
		work librarydb.Work
		want int64
	}{{first, firstPart.ID}, {second, secondPart.ID}} {
		got, err := queries.FindPartByAltNumber(t.Context(), librarydb.FindPartByAltNumberParams{
			WorkID: entry.work.ID, Scheme: "absolute", AltRef: "137",
		})
		if err != nil {
			t.Fatalf("FindPartByAltNumber for %q: %v", entry.work.CanonicalTitle, err)
		}
		if got.ID != entry.want {
			t.Errorf("absolute 137 in %q resolved to part %d, want %d: the lookup crossed works",
				entry.work.CanonicalTitle, got.ID, entry.want)
		}
	}
}

// Appendix E, D1. One file carrying twenty chapters satisfies twenty parts. With
// media_ref outside the key the second chapter collides with the first and the
// audiobook reads as one twentieth complete.
func TestOneFileCoversEveryChapterOfAnAudiobook(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "audiobook", "The Blade Itself")
	item := createDesired(t, opened, work.ID, "SET", 20)
	object := createObject(t, opened, "quick-audiobook", 512<<20)

	for chapter := 1; chapter <= 20; chapter++ {
		attach(t, opened, item.ID, object.ID, "primary", fmt.Sprintf("ch%02d", chapter))
	}

	covered, err := queries.CountPrimaryFiles(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("CountPrimaryFiles: %v", err)
	}
	if covered != 20 {
		t.Fatalf("one file covered %d of 20 chapters: a single-file audiobook cannot satisfy "+
			"a set unless media_ref is part of the key", covered)
	}

	// The coverage is twenty rows over one object, not twenty objects.
	files, err := queries.ListFilesForDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListFilesForDesiredItem: %v", err)
	}
	if len(files) != 20 {
		t.Errorf("the item lists %d files, want 20", len(files))
	}
	for _, file := range files {
		if file.MediaObjectID != object.ID {
			t.Errorf("coverage names object %d, want the single file %d",
				file.MediaObjectID, object.ID)
		}
	}
}

// Detaching one part leaves the rest of the file's coverage alone, which is what
// a file that turned out to carry less than it claimed needs.
func TestDetachingOnePartLeavesTheRestOfTheCoverage(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "comic", "Saga")
	item := createDesired(t, opened, work.ID, "SET", 6)
	object := createObject(t, opened, "quick-trade", 64<<20)

	for issue := 1; issue <= 6; issue++ {
		attach(t, opened, item.ID, object.ID, "primary", fmt.Sprintf("#%03d", issue))
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DetachFilePart(ctx, librarydb.DetachFilePartParams{
			DesiredItemID: item.ID, MediaObjectID: object.ID,
			Role: "primary", MediaRef: "#004",
		})
	})

	covered, err := queries.CountPrimaryFiles(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("CountPrimaryFiles: %v", err)
	}
	if covered != 5 {
		t.Errorf("the trade covers %d issues after detaching one, want 5", covered)
	}
}

// Appendix E, D2. A soloist credited on one track has not performed on every
// track, and a work-level listing that returned them would say so.
func TestAPartCreditIsNotAWorkCredit(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Symphony No. 5")
	part := createPart(t, opened, work.ID, "disc1:track02", "0002", "II. Andante")

	composer := createPerson(t, opened, "Mirevi ses Zuteferav")
	soloist := createPerson(t, opened, "Martha Argerich")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: composer.ID, WorkID: work.ID, Role: "composer",
		}); err != nil {
			return err
		}
		return q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: soloist.ID, WorkID: work.ID, PartID: part.ID, Role: "performer",
		})
	})

	workLevel, err := queries.ListCreditsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListCreditsForWork: %v", err)
	}
	if len(workLevel) != 1 {
		t.Fatalf("the work lists %d credits, want only the composer", len(workLevel))
	}
	if workLevel[0].PersonID != composer.ID {
		t.Errorf("the work-level credit is person %d, want the composer %d",
			workLevel[0].PersonID, composer.ID)
	}

	partLevel, err := queries.ListCreditsForPart(t.Context(), part.ID)
	if err != nil {
		t.Fatalf("ListCreditsForPart: %v", err)
	}
	if len(partLevel) != 1 || partLevel[0].PersonID != soloist.ID {
		t.Fatalf("the part lists %d credits, want the soloist %d", len(partLevel), soloist.ID)
	}

	// Coverage still sees both: the soloist did perform on this work.
	everything, err := queries.ListAllCreditsUnderWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListAllCreditsUnderWork: %v", err)
	}
	if len(everything) != 2 {
		t.Errorf("the work carries %d credits at any scope, want 2", len(everything))
	}
}

// The same boundary one level up: a narrator belongs to the audio edition, not to
// the book, and the book's credits must not gain one per edition ever released.
func TestAnEditionCreditIsNotAWorkCredit(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "book", "The Blade Itself")

	var audio librarydb.Edition
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateEdition(ctx, librarydb.CreateEditionParams{
			WorkID: work.ID, Kind: "audiobook", Label: "unabridged",
		})
		audio = row
		return err
	})

	author := createPerson(t, opened, "Joe Abercrombie")
	narrator := createPerson(t, opened, "Steven Pacey")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: author.ID, WorkID: work.ID, Role: "author",
		}); err != nil {
			return err
		}
		return q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: narrator.ID, WorkID: work.ID, EditionID: audio.ID, Role: "narrator",
		})
	})

	workLevel, err := queries.ListCreditsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListCreditsForWork: %v", err)
	}
	if len(workLevel) != 1 || workLevel[0].PersonID != author.ID {
		t.Fatalf("the book lists %d credits, want only the author %d", len(workLevel), author.ID)
	}

	editionLevel, err := queries.ListCreditsForEdition(t.Context(), audio.ID)
	if err != nil {
		t.Fatalf("ListCreditsForEdition: %v", err)
	}
	if len(editionLevel) != 1 || editionLevel[0].PersonID != narrator.ID {
		t.Errorf("the edition lists %d credits, want the narrator %d",
			len(editionLevel), narrator.ID)
	}
}

// One person can hold the same role on a work and on one of its parts without the
// two collapsing, which is what a nullable column in the key would have caused.
func TestTheSameRoleSurvivesAtTwoScopes(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Grinderman")
	part := createPart(t, opened, work.ID, "disc1:track03", "0003", "No Tepen Blues")
	person := createPerson(t, opened, "Nick Cave")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: person.ID, WorkID: work.ID, Role: "performer",
		}); err != nil {
			return err
		}
		return q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: person.ID, WorkID: work.ID, PartID: part.ID, Role: "performer",
		})
	})

	everything, err := queries.ListAllCreditsUnderWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListAllCreditsUnderWork: %v", err)
	}
	if len(everything) != 2 {
		t.Fatalf("the work carries %d credits, want 2: the scoped credit overwrote the "+
			"unscoped one", len(everything))
	}
}

// Appendix E, D3. Without per-part monitoring a season pack forces a season-level
// set, and "the season, but not the recap episode" cannot be said at all.
func TestUnmonitoringAPartShrinksWhatTheSetRequires(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "The Wire")

	var parts []librarydb.WorkPart
	for episode := 1; episode <= 10; episode++ {
		parts = append(parts, createPart(t, opened, work.ID,
			fmt.Sprintf("s03e%02d", episode), fmt.Sprintf("0003-%04d", episode), ""))
	}

	before, err := queries.CountMonitoredParts(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("CountMonitoredParts: %v", err)
	}
	if before != 10 {
		t.Fatalf("the season starts at %d monitored parts, want 10", before)
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetWorkPartMonitored(ctx, librarydb.SetWorkPartMonitoredParams{
			Monitored: 0, ID: parts[4].ID,
		})
	})

	after, err := queries.CountMonitoredParts(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("CountMonitoredParts: %v", err)
	}
	if after != 9 {
		t.Errorf("the season requires %d parts after unmonitoring one, want 9", after)
	}

	// The part is still there with its air date; only the intent changed.
	still, err := queries.GetWorkPart(t.Context(), parts[4].ID)
	if err != nil {
		t.Fatalf("GetWorkPart: %v", err)
	}
	if still.MediaRef != "s03e05" || still.Monitored != 0 {
		t.Errorf("the unmonitored part reads %+v, want s03e05 still present and unmonitored", still)
	}
}

// An episode keeps its air date whether or not anyone wants the file, which is
// what stops an unmonitored series rendering as an empty list.
func TestAnUnmonitoredPartKeepsItsAirDate(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "The Wire")

	var part librarydb.WorkPart
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: work.ID, MediaRef: "s03e05", SortKey: "0003-0005",
			Title:     sql.NullString{String: "Straight and True", Valid: true},
			Date:      sql.NullString{String: "2004-10-24", Valid: true},
			Monitored: 0,
		})
		part = row
		return err
	})

	got, err := queries.GetWorkPart(t.Context(), part.ID)
	if err != nil {
		t.Fatalf("GetWorkPart: %v", err)
	}
	want := librarydb.WorkPart{
		ID: part.ID, WorkID: work.ID, MediaRef: "s03e05", SortKey: "0003-0005",
		Title:     sql.NullString{String: "Straight and True", Valid: true},
		Date:      sql.NullString{String: "2004-10-24", Valid: true},
		Monitored: 0,
	}
	if got != want {
		t.Errorf("the part round-tripped as %+v, want %+v", got, want)
	}
}

// Parts belong to their work: deleting the work takes the tracklist, the titles
// and the numbering with it rather than leaving them to accumulate.
func TestDeletingAWorkTakesItsPartsWithIt(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	part := createPart(t, opened, work.ID, "disc1:track01", "0001", "The Furab Keke")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if _, err := q.AddWorkPartTitle(ctx, librarydb.AddWorkPartTitleParams{
			PartID: part.ID, Title: "Der Gnadenstuhl", Normalized: "der gnadenstuhl",
			Language: sql.NullString{String: "de", Valid: true},
			Provider: sql.NullString{String: "discogs", Valid: true},
		}); err != nil {
			return err
		}
		return q.AddAltNumbering(ctx, librarydb.AddAltNumberingParams{
			PartID: part.ID, Scheme: "medium", Provider: "musicbrainz", AltRef: "1",
		})
	})

	titles, err := queries.ListWorkPartTitles(t.Context(), part.ID)
	if err != nil {
		t.Fatalf("ListWorkPartTitles: %v", err)
	}
	if len(titles) != 1 {
		t.Fatalf("the part carries %d alternate titles, want 1", len(titles))
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteWork(ctx, work.ID)
	})

	if _, err := queries.GetWorkPart(t.Context(), part.ID); err == nil {
		t.Error("the part outlived its work")
	}
	remaining, err := queries.ListWorkPartTitles(t.Context(), part.ID)
	if err != nil {
		t.Fatalf("ListWorkPartTitles after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d alternate titles outlived the work", len(remaining))
	}
}
