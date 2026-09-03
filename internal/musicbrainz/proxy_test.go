package musicbrainz_test

import (
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/musicbrainz"
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

func TestProxy_NameIsTheBreakerBucketKey(t *testing.T) {
	source := musicbrainz.NewProxy(newTestClient(t, &fakeTransport{}, musicbrainz.ProxyName), "https://lidarr.invalid")
	if source.Name() != musicbrainz.ProxyName {
		t.Errorf("Name() = %q, want %q", source.Name(), musicbrainz.ProxyName)
	}
}
