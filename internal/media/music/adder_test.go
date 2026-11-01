package music_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	addedAlbumMBID  = "0b6f4a2e-1c3d-4e5f-8a9b-0c1d2e3f4a5b"
	addedArtistMBID = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	addLibraryID    = 7
)

func newAdder(t *testing.T, source musicbrainz.Source) (music.Adder, library.Desired, library.People, library.Works) {
	t.Helper()
	opened, identity, _, provider, fake := newResolveFixture(t, source)
	profileID := int64(4)
	desired, people := library.NewDesired(opened), library.NewPeople(opened)
	adder := music.Adder{
		Provider: provider, Identity: identity, Desired: desired, People: people,
		Categories:       fakeCategories{addLibraryID: settings.Category{ID: addLibraryID, MediaType: string(music.MediaType), QualityProfileID: &profileID}},
		DefaultProfileID: 1, Clock: fake,
	}
	return adder, desired, people, library.NewWorks(opened)
}

func TestAdder_AnAlbumIsCreatedFromTheSourceWithItsArtistAndAWantedItemOnTheLibrarysProfile(t *testing.T) {
	source := &fakeSource{rg: musicbrainz.ReleaseGroup{
		MBID: addedAlbumMBID, Title: "Velvet Quarry",
		ArtistCredit: []musicbrainz.ArtistCredit{{MBID: addedArtistMBID, Name: "Orla Fenwick"}},
	}}
	adder, desired, people, works := newAdder(t, source)
	var searched []int64
	adder.SearchAlbum = func(_ context.Context, _, itemID int64) { searched = append(searched, itemID) }

	request := media.AddRequest{
		CategoryID: addLibraryID, UserID: 3, Kind: music.AddKindAlbum, Provider: music.ProviderReleaseGroup,
		ExternalID: addedAlbumMBID, Monitored: true, Search: true,
	}
	first, err := adder.AddWork(t.Context(), request)
	if err != nil {
		t.Fatalf("AddWork: %v", err)
	}
	want := media.AddResult{Kind: music.AddKindAlbum, WorkID: first.WorkID, Searching: true, Created: true}
	if first.WorkID == 0 || first != want {
		t.Fatalf("AddWork = %+v, want %+v with a real work id", first, want)
	}

	detail, err := works.Detail(t.Context(), first.WorkID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail.Work.CanonicalTitle != "Velvet Quarry" || detail.Work.MediaType != string(music.MediaType) {
		t.Errorf("work = %q (%s), want the source's title as music", detail.Work.CanonicalTitle, detail.Work.MediaType)
	}
	if _, found, err := people.FindByExternalID(t.Context(), music.ProviderArtist, addedArtistMBID); err != nil || !found {
		t.Errorf("credited artist found = %v, %v; want the album's artist recorded at once", found, err)
	}
	items, err := desired.AlbumItemsForWork(t.Context(), first.WorkID)
	if err != nil {
		t.Fatalf("AlbumItemsForWork: %v", err)
	}
	if len(items) != 1 || items[0].CategoryID != addLibraryID || items[0].ProfileID != 4 || items[0].Monitored != 1 {
		t.Fatalf("items = %+v, want one monitored item in library %d on its profile 4", items, addLibraryID)
	}
	if len(searched) != 1 || searched[0] != items[0].ID {
		t.Errorf("searched = %v, want the new item %d", searched, items[0].ID)
	}

	again, err := adder.AddWork(t.Context(), request)
	if err != nil {
		t.Fatalf("AddWork again: %v", err)
	}
	if again.WorkID != first.WorkID || again.Created {
		t.Errorf("second add = %+v, want work %d found, not created", again, first.WorkID)
	}
}

func TestAdder_RefusesWhatItCannotAdd(t *testing.T) {
	notFound := &fakeSource{err: &httpx.StatusError{Status: http.StatusNotFound}}
	adder, _, _, _ := newAdder(t, notFound)

	cases := []struct {
		name    string
		request media.AddRequest
		want    error
	}{
		{"a mistyped id", media.AddRequest{CategoryID: addLibraryID, Provider: music.ProviderReleaseGroup, ExternalID: "not-an-mbid"}, media.ErrUnknownCandidate},
		{"an id no source knows", media.AddRequest{CategoryID: addLibraryID, Provider: music.ProviderReleaseGroup, ExternalID: addedAlbumMBID}, media.ErrUnknownCandidate},
		{"another type's provider", media.AddRequest{CategoryID: addLibraryID, Provider: "tvdb", ExternalID: "121361"}, media.ErrInvalidAdd},
		{"an unknown library", media.AddRequest{CategoryID: 99, Provider: music.ProviderReleaseGroup, ExternalID: addedAlbumMBID}, media.ErrInvalidAdd},
	}
	for _, tc := range cases {
		if _, err := adder.AddWork(t.Context(), tc.request); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if tc.name == "a mistyped id" && notFound.calls != 0 {
			t.Errorf("a mistyped id reached the source %d times, want it refused before", notFound.calls)
		}
	}
	if _, err := adder.LookupWorks(t.Context(), media.AddLookup{CategoryID: addLibraryID, Query: "x"}); !errors.Is(err, media.ErrQueryTooShort) {
		t.Errorf("one-letter lookup: err = %v, want ErrQueryTooShort", err)
	}
}
