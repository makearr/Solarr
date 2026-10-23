package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// A series' certification is stored with its age; a changed age has the planner look at its items again; a library's
// limit holds the works it does not admit, in the list and in the count, unrated ones too unless allowed.
func TestRatingLimit_HoldsWhatALibraryDoesNotAdmit(t *testing.T) {
	opened, queries := newLibrary(t)
	series := library.NewSeries(opened)
	desired := library.NewDesired(opened)
	refresh := func(title, tvdb, rating string, age int64) int64 {
		write := sampleSeries()
		write.Work.CanonicalTitle, write.Work.SortTitle = title, title
		write.IDs = []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: tvdb}}
		write.Certification, write.CertificationAge = rating, age
		result, err := series.Upsert(t.Context(), seriesAt, write)
		if err != nil {
			t.Fatal(err)
		}
		return result.WorkID
	}
	rated := func(title, tvdb, rating string, age int64) int64 {
		workID := refresh(title, tvdb, rating, age)
		if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: workID, MediaRef: "s01", CategoryID: 3, ProfileID: 1, Monitored: true}); err != nil {
			t.Fatal(err)
		}
		return workID
	}
	_, drama, _ := rated("Cartoon", "1", "TV-Y7", 7), rated("Drama", "2", "TV-MA", 17), rated("Unknown", "3", "XX", -1)

	items, err := queries.ListDesiredItemsForWork(t.Context(), drama)
	if err != nil || len(items) == 0 {
		t.Fatalf("items of the drama: %v, %v", items, err)
	}
	categoryID, age, err := desired.ItemRating(t.Context(), items[0].ID)
	if err != nil || categoryID != 3 || age != 17 {
		t.Fatalf("ItemRating = %d, %d, %v; want library 3, age 17", categoryID, age, err)
	}

	works := library.NewWorks(opened)
	held := func(limit library.RatingLimit) []string {
		rows, err := works.List(t.Context(), library.WorkFilter{AboveRating: &limit}, library.WorkCursor{}, 10)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		var titles []string
		for _, row := range rows {
			titles = append(titles, row.CanonicalTitle)
		}
		count, err := works.CountAboveRating(t.Context(), 3, limit)
		if err != nil || count != int64(len(titles)) {
			t.Fatalf("CountAboveRating = %d, %v; the list holds %v", count, err, titles)
		}
		return titles
	}
	if got := held(library.RatingLimit{MaxAge: 13}); len(got) != 2 || got[0] != "Drama" || got[1] != "Unknown" {
		t.Errorf("up to 13 holds %v, want Drama and Unknown", got)
	}
	if got := held(library.RatingLimit{MaxAge: 13, AllowUnrated: true}); len(got) != 1 || got[0] != "Drama" {
		t.Errorf("up to 13 with unrated holds %v, want Drama", got)
	}
	if got := held(library.RatingLimit{MaxAge: 18}); len(got) != 1 || got[0] != "Unknown" {
		t.Errorf("up to 18 holds %v, want Unknown", got)
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.MarkDesiredItemPlannedIfUnchanged(ctx, librarydb.MarkDesiredItemPlannedIfUnchangedParams{
			LastPlannedAt: sql.NullInt64{Int64: 1, Valid: true}, ID: items[0].ID, Generation: items[0].Generation, State: items[0].State, Monitored: items[0].Monitored,
		})
		return err
	})
	refresh("Drama", "2", "TV-MA", 17)
	if item, _ := queries.GetDesiredItem(t.Context(), items[0].ID); item.Dirty != 0 {
		t.Errorf("the same rating again marked the item dirty")
	}
	refresh("Drama", "2", "TV-14", 14)
	if item, _ := queries.GetDesiredItem(t.Context(), items[0].ID); item.Dirty != 1 {
		t.Errorf("a new rating left the item clean; the planner would not hold or release it")
	}
}

// Certifications stored before ages were read get their age from the media type's reading, once.
func TestBackfillCertificationAges_ReadsEachOnce(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Film")
	if err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO work_certification (work_id, system, rating) VALUES (?, 'movie', 'PG-13')", work.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	people := library.NewPeople(opened)
	ageOf := func(mediaType, rating string) int64 {
		if mediaType == "movie" && rating == "PG-13" {
			return 13
		}
		return -1
	}
	if read, err := people.BackfillCertificationAges(t.Context(), ageOf); err != nil || read != 1 {
		t.Fatalf("BackfillCertificationAges = %d, %v; want 1", read, err)
	}
	if read, err := people.BackfillCertificationAges(t.Context(), ageOf); err != nil || read != 0 {
		t.Fatalf("a second run read %d, %v; want none", read, err)
	}
	detail, err := library.NewWorks(opened).Detail(t.Context(), work.ID)
	if err != nil || detail.Rating != "PG-13" || detail.RatingAge != 13 {
		t.Fatalf("detail rating = %q age %d, %v; want PG-13 at 13", detail.Rating, detail.RatingAge, err)
	}
}
