package importlist_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/service/importlist"
)

type fakeHardcover struct {
	lists []hardcover.List
	books map[int64][]hardcover.ListBook
	err   error
}

func (f fakeHardcover) MyLists(context.Context) ([]hardcover.List, error) { return f.lists, f.err }
func (f fakeHardcover) ListBooks(_ context.Context, id int64, limit, offset int) ([]hardcover.ListBook, error) {
	all := f.books[id]
	if offset >= len(all) {
		return nil, nil
	}
	return all[offset:min(len(all), offset+limit)], nil
}

func TestHardcoverListFindsOneOfYourListsByNameSlugIdOrLink(t *testing.T) {
	client := fakeHardcover{
		lists: []hardcover.List{{ID: 7, Name: "To Read", Slug: "to-read"}, {ID: 8, Name: "Other", Slug: "other"}},
		books: map[int64][]hardcover.ListBook{7: {{ID: 11, Title: "One", Year: 2001}}},
	}
	want := []media.ListEntry{{Title: "One", Year: 2001, Form: "book", IDs: []media.ProviderID{{Provider: "hardcover", ExternalID: "11"}}}}
	for _, value := range []string{"to read", "to-read", "7", "https://hardcover.app/@zela/lists/to-read"} {
		got, err := importlist.HardcoverList{Client: client}.Entries(t.Context(), importlist.Options{"list": value})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %+v, %v", value, got, err)
		}
	}
	if _, err := (importlist.HardcoverList{Client: client}).Entries(t.Context(), importlist.Options{"list": "nobody's"}); !errors.Is(err, importlist.ErrListNotFound) {
		t.Errorf("a list that is not yours = %v, want ErrListNotFound", err)
	}
	_, err := importlist.HardcoverList{Client: fakeHardcover{err: hardcover.ErrNoKey}}.Entries(t.Context(), importlist.Options{"list": "x"})
	if importlist.FailureOf(err) != importlist.FailureNeedsKey {
		t.Errorf("without a token = %v, want needs_key", err)
	}
}

type fakeCollections struct {
	albums []importlist.CollectionAlbum
	err    error
	asked  string
}

func (f *fakeCollections) CollectionAlbums(_ context.Context, id string) ([]importlist.CollectionAlbum, error) {
	f.asked = id
	return f.albums, f.err
}
func (f *fakeCollections) IsNotFound(err error) bool { return errors.Is(err, errMissing) }

var errMissing = errors.New("missing")

func TestMusicBrainzCollectionTakesAnIdOrALinkAndNamesAlbumsByReleaseGroup(t *testing.T) {
	client := &fakeCollections{albums: []importlist.CollectionAlbum{{MBID: "rg-1", Title: "One"}}}
	got, err := importlist.MusicBrainzCollection{Client: client}.Entries(t.Context(), importlist.Options{"collection": "https://musicbrainz.org/collection/0A1B2C3D-0000-4000-8000-0123456789AB"})
	want := []media.ListEntry{{Title: "One", Form: "release-group", IDs: []media.ProviderID{{Provider: "musicbrainz:release-group", ExternalID: "rg-1"}}}}
	if err != nil || !reflect.DeepEqual(got, want) || client.asked != "0a1b2c3d-0000-4000-8000-0123456789ab" {
		t.Errorf("got %+v, %v asking %q", got, err, client.asked)
	}
	if _, err := (importlist.MusicBrainzCollection{Client: client}).Entries(t.Context(), importlist.Options{"collection": "https://example.com/x"}); !errors.Is(err, importlist.ErrListNotFound) {
		t.Errorf("a stranger's link = %v, want ErrListNotFound", err)
	}
	if _, err := (importlist.MusicBrainzCollection{Client: &fakeCollections{err: errMissing}}).Entries(t.Context(), importlist.Options{"collection": "0a1b2c3d-0000-4000-8000-0123456789ab"}); !errors.Is(err, importlist.ErrListNotFound) {
		t.Errorf("a private collection = %v, want ErrListNotFound", err)
	}
}
