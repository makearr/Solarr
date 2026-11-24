package music_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
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
		httpx.NewLimiter(wall, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
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

// A profile Lidarr's mapping chose for the artist is the person's choice: the album keeps it as an override of its own,
// where the library has a list of its own that would otherwise decide.
func TestLidarrImport_AMappedProfileIsKeptAsAnOverride(t *testing.T) {
	opened, identity, _, _, fake := newResolveFixture(t, &refusingSource{})
	libraryProfile, mappedProfile := int64(4), int64(9)
	desired := library.NewDesired(opened)
	importer := music.LidarrImport{
		Categories: fakeCategories{addLibraryID: settings.Category{ID: addLibraryID, MediaType: string(music.MediaType), QualityProfileID: &libraryProfile}},
		Identity:   identity, People: library.NewPeople(opened), Desired: desired, DefaultProfileID: 1, Clock: fake,
	}
	wall := clock.NewFakeAtEpoch()
	client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(wall, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(wall, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "lidarr-test", UserAgent: "test", Transport: lidarrAnswers{}})
	var recorded []library.NewArrImportItem
	run := arrmap.Run{
		Client:   arrimport.NewLidarrClient(client, "http://lidarr.local", "key"),
		Options:  arrmap.Options{Monitored: true, Albums: "all", ProfileMap: map[int64]*int64{2: &mappedProfile}},
		Place:    func(string) (int64, bool) { return addLibraryID, true },
		Progress: func(int64, int64) {},
		Record: func(_ context.Context, item library.NewArrImportItem) error {
			recorded = append(recorded, item)
			return nil
		},
		Link: func(context.Context, string, int64, string, int64) {},
	}
	if _, err := importer.Import(t.Context(), run); err != nil {
		t.Fatalf("Import: %v", err)
	}
	items, err := desired.AlbumItemsForWork(t.Context(), *recorded[1].WorkID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if got, found, err := desired.ProfileOverride(t.Context(), items[0].ID); err != nil || !found || got != mappedProfile {
		t.Errorf("override = %d, found %v, err %v; want the mapped profile %d", got, found, err, mappedProfile)
	}
}

// lidarrWithProfile is lidarrAnswers whose artist holds metadata profile 3: albums only, no live or remix.
type lidarrWithProfile struct{ lidarrAnswers }

func (served lidarrWithProfile) RoundTrip(request *http.Request) (*http.Response, error) {
	body := map[string]string{
		"/api/v1/artist": `[{"id":5,"artistName":"Orla Fenwick","foreignArtistId":"` + addedArtistMBID + `","monitored":true,
			"rootFolderPath":"/music","qualityProfileId":2,"metadataProfileId":3}]`,
		"/api/v1/config/mediamanagement": `{}`,
		"/api/v1/metadataprofile": `[{"id":3,"name":"Standard",
			"primaryAlbumTypes":[{"albumType":{"name":"Album"},"allowed":true},{"albumType":{"name":"EP"},"allowed":false}],
			"secondaryAlbumTypes":[{"albumType":{"name":"Studio"},"allowed":true},{"albumType":{"name":"Live"},"allowed":false},{"albumType":{"name":"Remix"},"allowed":false}]}]`,
	}[request.URL.Path]
	if body == "" {
		return served.lidarrAnswers.RoundTrip(request)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

// An artist's Lidarr metadata profile decides the release types its follow brings in; Studio is no exclusion.
func TestLidarrImport_TheArtistsMetadataProfileSetsTheFollowsReleaseTypes(t *testing.T) {
	opened, identity, _, _, fake := newResolveFixture(t, &refusingSource{})
	importer := music.LidarrImport{
		Categories: fakeCategories{addLibraryID: settings.Category{ID: addLibraryID, MediaType: string(music.MediaType), MonitorReleaseTypes: []string{"Album", "EP", "Single"}}},
		Identity:   identity, People: library.NewPeople(opened), Desired: library.NewDesired(opened), DefaultProfileID: 1, Clock: fake,
	}
	wall := clock.NewFakeAtEpoch()
	client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(wall, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(wall, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "lidarr-test", UserAgent: "test", Transport: lidarrWithProfile{}})
	var recorded []library.NewArrImportItem
	run := arrmap.Run{
		Client:   arrimport.NewLidarrClient(client, "http://lidarr.local", "key"),
		Options:  arrmap.Options{Monitored: true, Albums: "all"},
		Place:    func(string) (int64, bool) { return addLibraryID, true },
		Progress: func(int64, int64) {},
		Record: func(_ context.Context, item library.NewArrImportItem) error {
			recorded = append(recorded, item)
			return nil
		},
		Link: func(context.Context, string, int64, string, int64) {},
	}
	if _, err := importer.Import(t.Context(), run); err != nil {
		t.Fatalf("Import: %v", err)
	}
	follow, found, err := library.NewSubscriptions(opened).Get(t.Context(), *recorded[0].SubscriptionID)
	if err != nil || !found {
		t.Fatalf("follow = %v, %v", found, err)
	}
	got := music.ParseSubscriptionFilters(follow.FiltersJSON)
	if !reflect.DeepEqual(*got.IncludePrimaryTypes, []string{"Album"}) || !reflect.DeepEqual(*got.ExcludeSecondaryTypes, []string{"live", "remix"}) {
		t.Errorf("follow filters = %v / %v; want Album only, live and remix excluded", *got.IncludePrimaryTypes, *got.ExcludeSecondaryTypes)
	}
}

// Studio is Lidarr's "no secondary type", never an exclusion key, allowed or not.
func TestFiltersOfLidarrProfile_StudioIsNeverAnExclusion(t *testing.T) {
	var profile arrimport.LidarrMetadataProfile
	if err := json.Unmarshal([]byte(`{"primaryAlbumTypes":[{"albumType":{"name":"Album"},"allowed":true}],
		"secondaryAlbumTypes":[{"albumType":{"name":"Studio"},"allowed":false},{"albumType":{"name":"Demo"},"allowed":false}]}`), &profile); err != nil {
		t.Fatal(err)
	}
	got := music.FiltersOfLidarrProfile(profile)
	if !reflect.DeepEqual(*got.ExcludeSecondaryTypes, []string{"demo"}) {
		t.Errorf("excluded = %v, want [demo]", *got.ExcludeSecondaryTypes)
	}
}

// The preview carries, per Lidarr metadata profile, the release types its artists would be followed for.
func TestLidarrImport_PreviewNamesTheReleaseTypesOfEachMetadataProfile(t *testing.T) {
	wall := clock.NewFakeAtEpoch()
	client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(wall, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(wall, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "lidarr-test", UserAgent: "test", Transport: lidarrWithProfile{}})
	source, err := music.LidarrImport{}.Preview(t.Context(), arrimport.NewLidarrClient(client, "http://lidarr.local", "key"))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	want := map[int64]arrmap.PreviewFollow{3: {Name: "Standard", PrimaryTypes: []string{"Album"}, ExcludedSecondaryTypes: []string{"live", "remix"}}}
	if !reflect.DeepEqual(source.Follows, want) || source.Items[0].FollowProfileID != 3 {
		t.Errorf("follows = %+v, item profile %d; want %+v on profile 3", source.Follows, source.Items[0].FollowProfileID, want)
	}
}
