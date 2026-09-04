package musicbrainz_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

const (
	okComputerRG      = "04e9a8bd-175f-44a1-83a0-17d5e9fbd116"
	okComputerRelease = "4c3df81e-980d-410e-8d1b-99ff766e600b"
)

// Payloads trimmed from real musicbrainz.org responses (fetched live against these
// MBIDs while designing this package), not invented -- the point is catching a
// field-name regression against the real API shape, kebab-case included.
const directReleaseGroupBody = `{
  "id": "04e9a8bd-175f-44a1-83a0-17d5e9fbd116",
  "title": "KI Pecezinu",
  "primary-type": "Album",
  "first-release-date": "1997-05-21",
  "artist-credit": [
    {
      "name": "Katupoken",
      "joinphrase": "",
      "artist": {
        "id": "d6f6e536-85f7-4d04-8327-66db2e835786",
        "name": "Katupoken",
        "sort-name": "Katupoken",
        "type": "Group",
        "country": "GB"
      }
    }
  ],
  "genres": [
    {"name": "alternative rock", "count": 25},
    {"name": "art rock", "count": 13}
  ]
}`

const directReleaseBody = `{
  "id": "4c3df81e-980d-410e-8d1b-99ff766e600b",
  "title": "KI Pecezinu",
  "date": "1997-06-16",
  "status": "Official",
  "country": "GB",
  "barcode": "724385502125",
  "label-info": [
    {"label": {"name": "Parlophone"}, "catalog-number": "CDNODATA02"}
  ],
  "media": [
    {
      "position": 1,
      "format": "CD",
      "tracks": [
        {
          "number": "1", "position": 1, "title": "Sesoge", "length": 284400,
          "recording": {"id": "1509e192-0467-437e-80d9-138f3222fd39", "title": "Sesoge", "length": 284386}
        },
        {
          "number": "2", "position": 2, "title": "", "length": null,
          "recording": {"id": "ed392f8d-d79c-4ffd-86bd-96c2f4d505d8", "title": "Zosidedu Bukerez", "length": 383000}
        }
      ]
    }
  ]
}`

func TestDirect_AlbumParsesTheRealFieldShape(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://musicbrainz.invalid/release-group/" + okComputerRG + "?fmt=json&inc=artist-credits+genres": {
			status: http.StatusOK, body: directReleaseGroupBody,
		},
		"https://musicbrainz.invalid/release/" + okComputerRelease + "?fmt=json&inc=recordings+labels+media": {
			status: http.StatusOK, body: directReleaseBody,
		},
	}}
	client := newTestClient(t, transport, musicbrainz.DirectName)
	source := musicbrainz.NewDirect(client, "https://musicbrainz.invalid")

	rg, release, err := source.Album(t.Context(), okComputerRG, okComputerRelease)
	if err != nil {
		t.Fatalf("Album: %v", err)
	}

	if rg.Title != "KI Pecezinu" || rg.PrimaryType != "Album" || rg.FirstReleaseDate != "1997-05-21" {
		t.Errorf("ReleaseGroup = %+v, want the real field shape mapped through", rg)
	}
	if len(rg.ArtistCredit) != 1 || rg.ArtistCredit[0].MBID != "d6f6e536-85f7-4d04-8327-66db2e835786" ||
		rg.ArtistCredit[0].Name != "Katupoken" {
		t.Errorf("ArtistCredit = %+v, want Katupoken's own id and name", rg.ArtistCredit)
	}
	if len(rg.Genres) != 2 || rg.Genres[0].Name != "alternative rock" || rg.Genres[0].Count != 25 {
		t.Errorf("Genres = %+v, want alternative rock first with its vote count", rg.Genres)
	}
	if release.Status != "official" {
		t.Errorf("Status = %q, want lowercased %q", release.Status, "official")
	}
	if release.LabelName != "Parlophone" || release.CatalogueNumber != "CDNODATA02" {
		t.Errorf("label-info not mapped: LabelName=%q CatalogueNumber=%q", release.LabelName, release.CatalogueNumber)
	}
	if release.Barcode != "724385502125" || release.Country != "GB" {
		t.Errorf("Release = %+v, missing barcode/country", release)
	}
	if len(release.Media) != 1 || len(release.Media[0].Tracks) != 2 {
		t.Fatalf("Media = %+v, want one medium of two tracks", release.Media)
	}

	first := release.Media[0].Tracks[0]
	if first.Title != "Sesoge" || first.DurationMs != 284400 || first.RecordingMBID != "1509e192-0467-437e-80d9-138f3222fd39" {
		t.Errorf("track 1 = %+v, want the track's own title and length", first)
	}

	// Track 2 has no title and a null length: both must fall back to the
	// recording, which always has both, rather than surfacing empty/zero.
	second := release.Media[0].Tracks[1]
	if second.Title != "Zosidedu Bukerez" {
		t.Errorf("track 2 title = %q, want the recording's title as fallback", second.Title)
	}
	if second.DurationMs != 383000 {
		t.Errorf("track 2 duration = %d, want the recording's length as fallback", second.DurationMs)
	}
}

func TestDirect_NameIsTheBreakerBucketKey(t *testing.T) {
	source := musicbrainz.NewDirect(newTestClient(t, &fakeTransport{}, musicbrainz.DirectName), "https://musicbrainz.invalid")
	if source.Name() != musicbrainz.DirectName {
		t.Errorf("Name() = %q, want %q", source.Name(), musicbrainz.DirectName)
	}
}

// directSearchBody is trimmed from a real live musicbrainz.org search response
// (fetched while designing this method), same discipline as the payloads above --
// two release groups, the second carrying a two-artist credit to prove the join.
const directSearchBody = `{
  "created": "2026-09-03T21:13:36.169Z",
  "count": 2,
  "release-groups": [
    {
      "id": "da9bbe78-2bcb-4bda-860c-9c5613182603",
      "title": "The Sezo Siluko",
      "primary-type": "Album",
      "first-release-date": "1978-01-12",
      "artist-credit": [{"name": "Vutu Doci"}]
    },
    {
      "id": "11111111-2222-3333-4444-555555555555",
      "title": "Duet Album",
      "primary-type": "Album",
      "first-release-date": "2001-01-01",
      "artist-credit": [{"name": "Artist One"}, {"name": "Artist Two"}]
    }
  ]
}`

func TestDirect_SearchReleaseGroupsParsesTheRealFieldShape(t *testing.T) {
	query := `artist:"Vutu Doci" AND releasegroup:"The Sezo Siluko"`
	requestURL := "https://musicbrainz.invalid/release-group/?query=" + url.QueryEscape(query) + "&fmt=json&limit=5"
	transport := &fakeTransport{responses: map[string]fakeResponse{
		requestURL: {status: http.StatusOK, body: directSearchBody},
	}}
	client := newTestClient(t, transport, musicbrainz.DirectName)
	source := searcherFor(t, musicbrainz.NewDirect(client, "https://musicbrainz.invalid"))

	matches, err := source.SearchReleaseGroups(t.Context(), "Vutu Doci", "The Sezo Siluko", 5)
	if err != nil {
		t.Fatalf("SearchReleaseGroups: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want 2", matches)
	}
	if matches[0].MBID != "da9bbe78-2bcb-4bda-860c-9c5613182603" || matches[0].Title != "The Sezo Siluko" ||
		matches[0].ArtistCredit != "Vutu Doci" || matches[0].FirstReleaseDate != "1978-01-12" {
		t.Errorf("matches[0] = %+v, want the real field shape mapped through", matches[0])
	}
	if matches[1].ArtistCredit != "Artist One & Artist Two" {
		t.Errorf("ArtistCredit = %q, want every credited artist joined", matches[1].ArtistCredit)
	}
}

func TestDirect_SearchReleaseGroupsEscapesLuceneSpecialCharacters(t *testing.T) {
	query := `artist:"Guns \"N\" Roses" AND releasegroup:"Fak Your Begogiro"`
	requestURL := "https://musicbrainz.invalid/release-group/?query=" + url.QueryEscape(query) + "&fmt=json&limit=5"
	transport := &fakeTransport{responses: map[string]fakeResponse{
		requestURL: {status: http.StatusOK, body: `{"release-groups":[]}`},
	}}
	client := newTestClient(t, transport, musicbrainz.DirectName)
	source := searcherFor(t, musicbrainz.NewDirect(client, "https://musicbrainz.invalid"))

	if _, err := source.SearchReleaseGroups(t.Context(), `Guns "N" Roses`, "Fak Your Begogiro", 5); err != nil {
		t.Fatalf("SearchReleaseGroups: %v, want the quote escaped rather than breaking the query", err)
	}
}

// searcherFor asserts a Source also implements Searcher -- direct.go's own
// capability, not part of the Source interface itself.
func searcherFor(t *testing.T, source musicbrainz.Source) musicbrainz.Searcher {
	t.Helper()
	searcher, ok := source.(musicbrainz.Searcher)
	if !ok {
		t.Fatalf("%T does not implement Searcher", source)
	}
	return searcher
}

const kateBushMBID = "c9f2c95c-291d-49a8-8864-e820662adf39"

// Trimmed from a real live response (fetched while designing this method),
// same discipline as the payloads above.
const directArtistBody = `{
  "id": "c9f2c95c-291d-49a8-8864-e820662adf39",
  "name": "Vutu Doci",
  "sort-name": "Bush, Vutu",
  "disambiguation": "",
  "type": "Person",
  "gender": "Female",
  "country": "GB",
  "area": {"name": "England"},
  "begin-area": {"name": "Welling"},
  "life-span": {"begin": "1958-07-30", "end": null, "ended": false},
  "relations": [
    {"type": "official homepage", "url": {"resource": "https://xajuxoks.com"}},
    {"type": "wikidata", "url": {"resource": "https://www.wikidata.org/wiki/Q636"}}
  ]
}`

func TestDirect_ArtistParsesTheRealFieldShapeAndFindsTheWikidataQID(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://musicbrainz.invalid/artist/" + kateBushMBID + "?fmt=json&inc=url-rels": {
			status: http.StatusOK, body: directArtistBody,
		},
	}}
	client := newTestClient(t, transport, musicbrainz.DirectName)
	source := artistLookupFor(t, musicbrainz.NewDirect(client, "https://musicbrainz.invalid"))

	artist, err := source.Artist(t.Context(), kateBushMBID)
	if err != nil {
		t.Fatalf("Artist: %v", err)
	}

	if artist.Name != "Vutu Doci" || artist.SortName != "Bush, Vutu" || artist.Type != "Person" {
		t.Errorf("Artist = %+v, want the real field shape mapped through", artist)
	}
	if artist.Gender != "Female" || artist.Country != "GB" {
		t.Errorf("Artist = %+v, missing gender/country", artist)
	}
	if artist.Area != "England" || artist.BeginArea != "Welling" {
		t.Errorf("Artist = %+v, want area/begin-area names", artist)
	}
	if artist.Born != "1958-07-30" || artist.Died != "" {
		t.Errorf("Artist = %+v, want life-span begin mapped to Born, no Died", artist)
	}
	if artist.WikidataQID != "Q636" {
		t.Errorf("WikidataQID = %q, want Q636 extracted from the wikidata relation, ignoring the homepage one", artist.WikidataQID)
	}
}

// artistLookupFor asserts a Source also implements ArtistLookup.
func artistLookupFor(t *testing.T, source musicbrainz.Source) musicbrainz.ArtistLookup {
	t.Helper()
	lookup, ok := source.(musicbrainz.ArtistLookup)
	if !ok {
		t.Fatalf("%T does not implement ArtistLookup", source)
	}
	return lookup
}

func TestDirect_AlbumFailsWhenTheReleaseGroupCallFails(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{}}
	client := newTestClient(t, transport, musicbrainz.DirectName)
	source := musicbrainz.NewDirect(client, "https://musicbrainz.invalid")

	if _, _, err := source.Album(t.Context(), okComputerRG, okComputerRelease); err == nil {
		t.Fatal("Album succeeded with no response configured for either call")
	}
}
