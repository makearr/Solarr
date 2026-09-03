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

// TestWorkDetail_RoundTripsEveryLevel builds a work with an edition, a part and
// external ids at all three levels plus a root folder link, then asserts Detail
// reports every field back -- a right shape for the wrong reason is a bug here
// (section 11), so the whole struct is compared, not one field at a time.
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
		RootFolderIDs: []int64{7},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detail = %+v, want %+v", got, want)
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
