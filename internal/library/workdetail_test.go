package library_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// Builds a work with an edition, a part and external ids at every level, then
// asserts Detail reports the whole struct back, not just one field.
func TestWorkDetail_RoundTripsEveryLevel(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	work := createWork(t, opened, "music", "KI Pecezinu")

	var edition librarydb.Edition
	var part librarydb.WorkPart
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "musicbrainz:release-group", ExternalID: "rg-1", WorkID: work.ID,
			Confidence: 1, FirstSeenAt: firstSeen.Unix(),
		}); err != nil {
			return err
		}

		created, err := q.CreateEdition(ctx, librarydb.CreateEditionParams{
			WorkID: work.ID, Kind: "official", Label: "release-1",
			Year:   sql.NullInt64{Int64: 1997, Valid: true},
			Format: sql.NullString{String: "CD", Valid: true},
		})
		if err != nil {
			return err
		}
		edition = created

		if err := q.LinkEditionExternalID(ctx, librarydb.LinkEditionExternalIDParams{
			Provider: "musicbrainz:release", ExternalID: "release-1", EditionID: edition.ID, Confidence: 1,
		}); err != nil {
			return err
		}

		createdPart, err := q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: work.ID, MediaRef: "01", SortKey: "01",
			Title: sql.NullString{String: "Sesoge", Valid: true}, Monitored: 1,
		})
		if err != nil {
			return err
		}
		part = createdPart

		if err := q.LinkWorkPartExternalID(ctx, librarydb.LinkWorkPartExternalIDParams{
			Provider: "musicbrainz:recording", ExternalID: "rec-1", PartID: part.ID, Confidence: 1,
		}); err != nil {
			return err
		}

		return q.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{
			WorkID: work.ID, RootFolderID: 7, FirstSeenAt: firstSeen.Unix(),
		})
	})

	got, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}

	want := library.WorkDetail{
		Work: work,
		ExternalIDs: []librarydb.WorkExternalID{
			{Provider: "musicbrainz:release-group", ExternalID: "rg-1", WorkID: work.ID,
				Confidence: 1, FirstSeenAt: firstSeen.Unix()},
		},
		Editions: []library.EditionDetail{
			{
				Edition: edition,
				ExternalIDs: []librarydb.EditionExternalID{
					{Provider: "musicbrainz:release", ExternalID: "release-1", EditionID: edition.ID, Confidence: 1},
				},
			},
		},
		Parts: []library.PartDetail{
			{
				Part: part,
				ExternalIDs: []librarydb.WorkPartExternalID{
					{Provider: "musicbrainz:recording", ExternalID: "rec-1", PartID: part.ID, Confidence: 1},
				},
			},
		},
		Credits:       []library.Credit{},
		Genres:        []string{},
		RootFolderIDs: []int64{7},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detail = %+v, want %+v", got, want)
	}
}

func TestWorkDetail_IncludesCreditsAndGenres(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	people := library.NewPeople(opened)
	work := createWork(t, opened, "music", "KI Pecezinu")

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-1",
		Person: library.PersonIdentity{Name: "Katupoken", SortName: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), work.ID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	if err := people.AssignGenres(t.Context(), work.ID, []library.GenreLink{{Name: "alternative rock"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}

	got, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(got.Credits) != 1 || got.Credits[0].PersonID != personID ||
		got.Credits[0].PersonName != "Katupoken" || got.Credits[0].Role != "artist" {
		t.Errorf("Credits = %+v, want Katupoken's artist credit with the person's name", got.Credits)
	}
	if len(got.Genres) != 1 || got.Genres[0] != "alternative rock" {
		t.Errorf("Genres = %+v, want the one assigned genre", got.Genres)
	}
}

func TestWorkDetail_OnDiskReflectsRootFolderLinkage(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	work := createWork(t, opened, "music", "KI Pecezinu")

	unlinked, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if unlinked.OnDisk() {
		t.Error("OnDisk() = true for a work with no root folder link at all")
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{
			WorkID: work.ID, RootFolderID: 7, FirstSeenAt: firstSeen.Unix(),
		})
	})

	linked, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if !linked.OnDisk() {
		t.Error("OnDisk() = false for a work linked to a root folder")
	}
}

func TestWorkDetail_MissingWorkReportsNoRows(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	_, err := works.Detail(t.Context(), 404)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Detail on a missing id = %v, want an error wrapping sql.ErrNoRows", err)
	}
}
