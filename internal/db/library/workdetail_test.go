package library_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
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
		CategoryIDs:   []int64{},
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
		Person: library.PersonIdentity{Name: "Katupoken"},
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

func TestWorkDetail_WantedIsNilUntilSomethingWantsTheAlbum(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "KI Pecezinu")

	unwanted, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if unwanted.Wanted != nil {
		t.Errorf("Wanted = %+v, want nil before anything wants this album", unwanted.Wanted)
	}

	item, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}

	wanted, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if wanted.Wanted == nil {
		t.Fatal("Wanted = nil, want the album item just created")
	}
	if wanted.Wanted.ID != item.ID || !wanted.Wanted.Monitored || wanted.Wanted.State != "MISSING" {
		t.Errorf("Wanted = %+v, unexpected shape", wanted.Wanted)
	}
	if wanted.Wanted.RequiredCount != nil {
		t.Errorf("RequiredCount = %v, want nil before any work_part exists", wanted.Wanted.RequiredCount)
	}
}

// A track's own SINGLE item (todo 4.4) attaches to that part's own Wanted,
// not the whole work's -- distinct from the album-level SET one level up,
// and from Part.Monitored, which is a different flag entirely.
func TestWorkDetail_PartWantedIsNilUntilSomethingWantsThatTrackOnItsOwn(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "KI Pecezinu")
	wantedTrack := createPart(t, opened, work.ID, "disc1:track1", "00010001", "Sesoge")
	otherTrack := createPart(t, opened, work.ID, "disc1:track2", "00010002", "Zosidedu Bukerez")

	before, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	for _, part := range before.Parts {
		if part.Wanted != nil {
			t.Errorf("part %s: Wanted = %+v, want nil before anything wants any track", part.Part.MediaRef, part.Wanted)
		}
	}

	item, err := desired.EnsureTrackItemByPart(t.Context(), work.ID, wantedTrack.ID, 1, 1, nil)
	if err != nil {
		t.Fatalf("EnsureTrackItemByPart: %v", err)
	}

	after, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	for _, part := range after.Parts {
		switch part.Part.ID {
		case wantedTrack.ID:
			if part.Wanted == nil || part.Wanted.ID != item.ID {
				t.Errorf("wanted track: Wanted = %+v, want the item just created", part.Wanted)
			}
		case otherTrack.ID:
			if part.Wanted != nil {
				t.Errorf("other track: Wanted = %+v, want nil -- only its own track was wanted", part.Wanted)
			}
		}
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

// The album detail used to disagree with the artist overview: present_count
// said the album was complete while every track rendered as missing, because
// nothing carried per-part presence to the detail view (reported 2026-09-19).
func TestWorkDetail_ReportsWhichPartsHaveAFile(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	work := createWork(t, opened, "music", "Tender Prey")

	var present, absent librarydb.WorkPart
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		created, err := q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: work.ID, MediaRef: "01", SortKey: "01",
			Title: sql.NullString{String: "The Furab Keke", Valid: true}, Monitored: 1,
		})
		if err != nil {
			return err
		}
		present = created

		created, err = q.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: work.ID, MediaRef: "02", SortKey: "02",
			Title: sql.NullString{String: "Up Jumped the Devil", Valid: true}, Monitored: 1,
		})
		absent = created
		return err
	})

	item := createDesired(t, opened, work.ID, "SET", 2)
	object := createObject(t, opened, "quick-mercy", 4096)
	attach(t, opened, item.ID, object.ID, "primary", present.MediaRef)

	got, err := works.Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(got.Parts) != 2 {
		t.Fatalf("Parts = %d, want 2", len(got.Parts))
	}
	if got.Parts[0].Part.ID != present.ID || got.Parts[0].File == nil ||
		got.Parts[0].File.MediaObjectID != object.ID {
		t.Errorf("part %q File = %+v, want the attached media object %d",
			present.MediaRef, got.Parts[0].File, object.ID)
	}
	if got.Parts[1].Part.ID != absent.ID || got.Parts[1].File != nil {
		t.Errorf("part %q File = %+v, want nil -- nothing is attached to it",
			absent.MediaRef, got.Parts[1].File)
	}
}
