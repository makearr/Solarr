package musicbrainz_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
)

// Trimmed from a real api.lidarr.audio/api/v0.4/album response (fetched live
// against okComputerRG while designing this package). Casing is exactly as the
// service answers -- lowercase "id"/"title"/"releasedate", capitalised
// "Releases"/"Id"/"Tracks" -- confirmed, not a typo carried over from direct.
const proxyAlbumBody = `{
  "id": "04e9a8bd-175f-44a1-83a0-17d5e9fbd116",
  "title": "KI Pecezinu",
  "type": "Album",
  "releasedate": "1997-05-21",
  "Releases": [
    {
      "Id": "4c3df81e-980d-410e-8d1b-99ff766e600b",
      "Title": "KI Pecezinu",
      "Status": "Official",
      "Country": ["United Kingdom"],
      "Label": ["Parlophone"],
      "Media": [{"Position": 1, "Name": "CD", "Format": "CD"}],
      "ReleaseDate": "1997-06-16",
      "TrackCount": 2,
      "Tracks": [
        {
          "Id": "dfbd3c25-3867-4c33-804d-caf379a0ca36", "RecordingId": "1509e192-0467-437e-80d9-138f3222fd39",
          "TrackName": "Sesoge", "TrackNumber": "1", "TrackPosition": 1, "DurationMs": 284400, "MediumNumber": 1
        },
        {
          "Id": "b7c1a222-1111-2222-3333-444455556666", "RecordingId": "ed392f8d-d79c-4ffd-86bd-96c2f4d505d8",
          "TrackName": "Zosidedu Bukerez", "TrackNumber": "2", "TrackPosition": 2, "DurationMs": 383000, "MediumNumber": 1
        }
      ]
    },
    {
      "Id": "11111111-2222-3333-4444-555555555555",
      "Title": "KI Pecezinu (cassette)",
      "Status": "Official",
      "Country": ["United Kingdom"],
      "Label": ["Parlophone"],
      "Media": [{"Position": 1, "Name": "Cassette", "Format": "Cassette"}],
      "ReleaseDate": "1997-06-16",
      "TrackCount": 1,
      "Tracks": [
        {
          "Id": "22222222-3333-4444-5555-666666666666", "RecordingId": "1509e192-0467-437e-80d9-138f3222fd39",
          "TrackName": "Sesoge", "TrackNumber": "1", "TrackPosition": 1, "DurationMs": 284400, "MediumNumber": 1
        }
      ]
    }
  ]
}`

func TestProxy_AlbumPicksTheMatchingReleaseOutOfSeveral(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://lidarr.invalid/album/" + okComputerRG: {status: http.StatusOK, body: proxyAlbumBody},
	}}
	client := newTestClient(t, transport, musicbrainz.ProxyName)
	source := musicbrainz.NewProxy(client, "https://lidarr.invalid")

	rg, release, err := source.Album(t.Context(), okComputerRG, okComputerRelease)
	if err != nil {
		t.Fatalf("Album: %v", err)
	}

	if rg.Title != "KI Pecezinu" || rg.PrimaryType != "Album" || rg.FirstReleaseDate != "1997-05-21" {
		t.Errorf("ReleaseGroup = %+v, want the album-level fields", rg)
	}
	// The response names two releases; the CD, not the cassette, must come back.
	if release.MBID != okComputerRelease || release.Title != "KI Pecezinu" {
		t.Errorf("Release = %+v, want the release matching releaseMBID, not the other one in Releases[]", release)
	}
	if release.Status != "official" || release.Country != "United Kingdom" || release.LabelName != "Parlophone" {
		t.Errorf("Release fields = %+v, not mapped from the matched entry", release)
	}
	if len(release.Media) != 1 || len(release.Media[0].Tracks) != 2 {
		t.Fatalf("Media = %+v, want the CD release's own two tracks, not the cassette's one", release.Media)
	}
	if release.Media[0].Format != "CD" {
		t.Errorf("Format = %q, want the medium matched by MediumNumber/Position", release.Media[0].Format)
	}
	if release.Media[0].Tracks[0].RecordingMBID != "1509e192-0467-437e-80d9-138f3222fd39" {
		t.Errorf("track 1 RecordingMBID not mapped from RecordingId")
	}
}

func TestProxy_AlbumErrorsWhenTheReleaseIsNotUnderTheGroup(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://lidarr.invalid/album/" + okComputerRG: {status: http.StatusOK, body: proxyAlbumBody},
	}}
	client := newTestClient(t, transport, musicbrainz.ProxyName)
	source := musicbrainz.NewProxy(client, "https://lidarr.invalid")

	if _, _, err := source.Album(t.Context(), okComputerRG, "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("Album succeeded for a release MBID the response never mentions")
	}
}

// An empty releaseMBID answers the release group alone -- section 21.3's
// manual-resolve action only ever has a release group id, never a specific
// pressing, to confirm a queued file against.
// Trimmed from the real api.lidarr.audio/api/v0.4/album response for Heka's
// Detisisu Binip, captured 2026-09-17: only the artist, genre and link fields,
// which proxyAlbumBody above leaves out -- why no test noticed they were never read.
const proxyAlbumWithArtistsBody = `{
  "id": "fee9cdd9-fa88-4fda-8fc0-0fc37765bfb5",
  "title": "Detisisu Binip",
  "type": "Album",
  "secondarytypes": [],
  "releasedate": "1991-11-04",
  "artistid": "9546cacb-d515-4327-878c-de2125eb70af",
  "artists": [
    {
      "id": "9546cacb-d515-4327-878c-de2125eb70af",
      "artistname": "Heka",
      "sortname": "Heka"
    }
  ],
  "genres": [
    "New Age",
    "Zahudu New Age",
    "Ambient"
  ],
  "links": [
    {
      "target": "https://www.discogs.com/master/27537",
      "type": "discogs"
    },
    {
      "target": "https://www.wikidata.org/wiki/Q1049433",
      "type": "wikidata"
    }
  ],
  "Releases": []
}`

// An album enriched through the proxy must carry its artist, or a fresh library
// ends up with no artists at all; genres and the Wikidata bridge ride along.
func TestProxy_AlbumReadsArtistsGenresAndWikidata(t *testing.T) {
	const shepherdMoons = "fee9cdd9-fa88-4fda-8fc0-0fc37765bfb5"
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://lidarr.invalid/album/" + shepherdMoons: {status: http.StatusOK, body: proxyAlbumWithArtistsBody},
	}}
	source := musicbrainz.NewProxy(newTestClient(t, transport, musicbrainz.ProxyName), "https://lidarr.invalid")

	rg, _, err := source.Album(t.Context(), shepherdMoons, "")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	wantCredit := []musicbrainz.ArtistCredit{{MBID: "9546cacb-d515-4327-878c-de2125eb70af", Name: "Heka"}}
	if !slices.Equal(rg.ArtistCredit, wantCredit) {
		t.Errorf("ArtistCredit = %+v, want %+v", rg.ArtistCredit, wantCredit)
	}
	wantGenres := []musicbrainz.Genre{{Name: "New Age"}, {Name: "Zahudu New Age"}, {Name: "Ambient"}}
	if !slices.Equal(rg.Genres, wantGenres) {
		t.Errorf("Genres = %+v, want %+v", rg.Genres, wantGenres)
	}
	if rg.WikidataQID != "Q1049433" {
		t.Errorf("WikidataQID = %q, want Q1049433", rg.WikidataQID)
	}
}

// Credit order decides a work's primary artist, so the album's own artistid
// leads even when the proxy lists a featured artist first.
func TestProxy_AlbumPutsItsPrimaryArtistFirst(t *testing.T) {
	const body = `{"id": "rg-1", "title": "Duet", "type": "Album", "artistid": "primary",
		"artists": [{"id": "guest", "artistname": "Guest"}, {"id": "primary", "artistname": "Primary"}],
		"Releases": []}`
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://lidarr.invalid/album/rg-1": {status: http.StatusOK, body: body},
	}}
	source := musicbrainz.NewProxy(newTestClient(t, transport, musicbrainz.ProxyName), "https://lidarr.invalid")

	rg, _, err := source.Album(t.Context(), "rg-1", "")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	want := []musicbrainz.ArtistCredit{{MBID: "primary", Name: "Primary"}, {MBID: "guest", Name: "Guest"}}
	if !slices.Equal(rg.ArtistCredit, want) {
		t.Errorf("ArtistCredit = %+v, want %+v", rg.ArtistCredit, want)
	}
}

func TestProxy_AlbumWithNoReleaseMBIDAnswersTheReleaseGroupAlone(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://lidarr.invalid/album/" + okComputerRG: {status: http.StatusOK, body: proxyAlbumBody},
	}}
	client := newTestClient(t, transport, musicbrainz.ProxyName)
	source := musicbrainz.NewProxy(client, "https://lidarr.invalid")

	rg, release, err := source.Album(t.Context(), okComputerRG, "")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	if rg.Title != "KI Pecezinu" {
		t.Errorf("ReleaseGroup = %+v, want the release group's own fields still populated", rg)
	}
	if release.MBID != "" || release.Title != "" || len(release.Media) != 0 {
		t.Errorf("Release = %+v, want a zero value when no releaseMBID was asked for", release)
	}
}

func TestProxy_NameIsTheBreakerBucketKey(t *testing.T) {
	source := musicbrainz.NewProxy(newTestClient(t, &fakeTransport{}, musicbrainz.ProxyName), "https://lidarr.invalid")
	if source.Name() != musicbrainz.ProxyName {
		t.Errorf("Name() = %q, want %q", source.Name(), musicbrainz.ProxyName)
	}
}
