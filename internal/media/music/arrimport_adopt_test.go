package music_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// lidarrAnswers is a Lidarr with one monitored artist and two albums, the second unmonitored.
type lidarrAnswers struct{}

func (lidarrAnswers) RoundTrip(request *http.Request) (*http.Response, error) {
	body := map[string]string{
		"/api/v1/system/status": `{"appName":"Lidarr","version":"2"}`,
		"/api/v1/artist": `[{"id":5,"artistName":"Orla Fenwick","foreignArtistId":"` + addedArtistMBID + `","monitored":true,
			"rootFolderPath":"/music","qualityProfileId":2}]`,
		"/api/v1/album": `[{"id":8,"title":"Velvet Quarry","foreignAlbumId":"` + addedAlbumMBID + `","monitored":true},
			{"id":9,"title":"Unwanted","foreignAlbumId":"1d2e3f4a-0b6f-4a2e-8c3d-4e5f8a9b0c1d","monitored":false}]`,
	}[request.URL.Path]
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

// refusingSource fails any question: the adoption must get by on Lidarr's record alone.
type refusingSource struct{ fakeSource }

func (*refusingSource) ReleaseGroup(context.Context, string) (musicbrainz.ReleaseGroup, error) {
	return musicbrainz.ReleaseGroup{}, errors.New("the import asked MusicBrainz")
}

// A Lidarr import adopts what Lidarr's record names, without asking any metadata source, and answers what it created so
// a revert can take it back.
func TestLidarrImport_AdoptsFromLidarrsRecordAndNamesWhatItCreated(t *testing.T) {
	opened, identity, _, _, fake := newResolveFixture(t, &refusingSource{})
	profileID := int64(4)
	desired, people := library.NewDesired(opened), library.NewPeople(opened)
	importer := music.LidarrImport{
		Categories: fakeCategories{addLibraryID: settings.Category{ID: addLibraryID, MediaType: string(music.MediaType), QualityProfileID: &profileID}},
		Identity:   identity, People: people, Desired: desired, DefaultProfileID: 1, Clock: fake,
	}
	wall := clock.NewFakeAtEpoch()
	client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(wall, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(wall, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "lidarr-test", UserAgent: "test", Transport: lidarrAnswers{}})

	var recorded []library.NewArrImportItem
	links := map[string]int64{}
	run := arrmap.Run{
		Client:   arrimport.NewLidarrClient(client, "http://lidarr.local", "key"),
		Options:  arrmap.Options{Monitored: true, Albums: "all"},
		Place:    func(string) (int64, bool) { return addLibraryID, true },
		Progress: func(int64, int64) {},
		Record: func(_ context.Context, item library.NewArrImportItem) error {
			recorded = append(recorded, item)
			return nil
		},
		Link: func(_ context.Context, kind string, _ int64, _ string, localID int64) { links[kind] = localID },
	}
	counts, err := importer.Import(t.Context(), run)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if want := (library.ArrImportCounts{ArtistsCreated: 1, AlbumsCreated: 1}); counts != want {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}
	if len(recorded) != 2 || !recorded[0].PersonCreated || recorded[0].SubscriptionID == nil || !recorded[1].WorkCreated {
		t.Fatalf("recorded = %+v, want a created followed person and a created work", recorded)
	}
	items, err := desired.AlbumItemsForWork(t.Context(), *recorded[1].WorkID)
	if err != nil || len(items) != 1 || items[0].ProfileID != profileID || items[0].CategoryID != addLibraryID || items[0].Monitored != 1 {
		t.Errorf("wanted items = %+v, %v; want one monitored in the library on its profile", items, err)
	}
	if links[arrmap.LinkArtist] != *recorded[0].PersonID || links[arrmap.LinkAlbum] != *recorded[1].WorkID {
		t.Errorf("links = %v, want the artist and the album kept for sending monitoring back", links)
	}

	again, err := importer.Import(t.Context(), run)
	if err != nil || again != (library.ArrImportCounts{ArtistsMatched: 1, AlbumsMatched: 1}) {
		t.Errorf("second import = %+v, %v; want everything matched", again, err)
	}
}
