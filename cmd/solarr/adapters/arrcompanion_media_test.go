package adapters

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// scriptedArr answers a GET by the longest scripted path prefix and records every write; it is never a real *Arr.
type scriptedArr struct {
	answers map[string]string
	writes  []string
}

func (s *scriptedArr) RoundTrip(request *http.Request) (*http.Response, error) {
	body := "[]"
	if request.Method == http.MethodGet {
		best := ""
		for path, answer := range s.answers {
			if strings.HasPrefix(request.URL.Path, path) && len(path) > len(best) {
				best, body = path, answer
			}
		}
	} else {
		payload, _ := io.ReadAll(request.Body)
		s.writes = append(s.writes, request.Method+" "+request.URL.Path+" "+string(payload))
		body = `{"id":5}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (s *scriptedArr) joined() string { return strings.Join(s.writes, "\n") }

func lidarrOver(transport http.RoundTripper) arrimport.LidarrClient {
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return arrimport.NewLidarrClient(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "companion-test", UserAgent: "test", Transport: transport,
	}), "http://arr.local", "key")
}

var musicShelf = map[string]string{
	"/api/v1/artist": `[{"id":1,"qualityProfileId":2,"metadataProfileId":3,"rootFolderPath":"/shelf"}]`,
}

func TestCompanionMusic_AddsWhatLidarrLacksAsTheDialogChose(t *testing.T) {
	target := mediaRef{ref: "0000-album", linkKind: "album"}
	for name, tc := range map[string]struct {
		added apiserver.AddedWork
		want  []string
	}{
		"unmonitored":          {apiserver.AddedWork{}, []string{"POST /api/v1/album", `"monitored":false`, `"searchForNewAlbum":false`, `"qualityProfileId":2`, `"metadataProfileId":3`, `"rootFolderPath":"/shelf"`}},
		"monitored and search": {apiserver.AddedWork{Monitored: true, Search: true}, []string{`"monitored":true`, `"searchForNewAlbum":true`}},
	} {
		answers := map[string]string{"/api/v1/album/lookup": `[{"foreignAlbumId":"0000-album","artist":{"foreignArtistId":"0000-artist"}}]`}
		for path, answer := range musicShelf {
			answers[path] = answer
		}
		arr := &scriptedArr{answers: answers}
		id, err := (ArrSync{}).companionMusic(t.Context(), lidarrOver(arr), target, tc.added)
		if err != nil || id != 5 {
			t.Fatalf("%s: id %d, error %v; want Lidarr's new id 5", name, id, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(arr.joined(), want) {
				t.Errorf("%s: writes = %s, want %s", name, arr.joined(), want)
			}
		}
	}
}

func TestCompanionMusic_AHeldAlbumIsMonitoredAndSearchedOnlyWhenAsked(t *testing.T) {
	target := mediaRef{ref: "0000-album", linkKind: "album"}
	answers := map[string]string{"/api/v1/album/lookup": `[{"id":7,"foreignAlbumId":"0000-album"}]`, "/api/v1/album/7": `{"id":7,"monitored":false}`}
	for name, tc := range map[string]struct {
		added apiserver.AddedWork
		want  []string
		never string
	}{
		"added unmonitored: left as it is": {apiserver.AddedWork{}, nil, "PUT"},
		"monitored: set, not searched":     {apiserver.AddedWork{Monitored: true}, []string{"PUT /api/v1/album/7", `"monitored":true`}, "AlbumSearch"},
		"monitored and searched: both":     {apiserver.AddedWork{Monitored: true, Search: true}, []string{"PUT /api/v1/album/7", `"albumIds":[7]`}, ""},
	} {
		arr := &scriptedArr{answers: answers}
		if id, err := (ArrSync{}).companionMusic(t.Context(), lidarrOver(arr), target, tc.added); err != nil || id != 7 {
			t.Fatalf("%s: id %d, error %v", name, id, err)
		}
		if tc.want == nil && arr.joined() != "" {
			t.Errorf("%s: wrote %s, want nothing", name, arr.joined())
		}
		for _, want := range tc.want {
			if !strings.Contains(arr.joined(), want) {
				t.Errorf("%s: writes = %s, want %s", name, arr.joined(), want)
			}
		}
		if tc.never != "" && strings.Contains(arr.joined(), tc.never) {
			t.Errorf("%s: writes = %s, must not contain %s", name, arr.joined(), tc.never)
		}
	}
}

func TestCompanionMusic_AnArtistGoesToTheArtistEndpoints(t *testing.T) {
	arr := &scriptedArr{answers: map[string]string{"/api/v1/artist/lookup": `[{"id":4,"foreignArtistId":"0000-artist"}]`, "/api/v1/artist/4": `{"id":4}`}}
	if id, err := (ArrSync{}).companionMusic(t.Context(), lidarrOver(arr), mediaRef{ref: "0000-artist", linkKind: "artist"}, apiserver.AddedWork{Monitored: true, Search: true}); err != nil || id != 4 {
		t.Fatalf("id %d, error %v", id, err)
	}
	for _, want := range []string{"PUT /api/v1/artist/4", `"name":"ArtistSearch"`, `"artistId":4`} {
		if !strings.Contains(arr.joined(), want) {
			t.Errorf("writes = %s, want %s", arr.joined(), want)
		}
	}
}

func TestCompanionMusic_ALookupThatFindsNothingIsAnError(t *testing.T) {
	arr := &scriptedArr{answers: map[string]string{}}
	if _, err := (ArrSync{}).companionMusic(t.Context(), lidarrOver(arr), mediaRef{ref: "x", linkKind: "album"}, apiserver.AddedWork{Monitored: true}); err == nil || arr.joined() != "" {
		t.Errorf("error %v, writes %q; want an error and no write", err, arr.joined())
	}
}

func TestCompanionScene_PicksTheSceneWithTheProvidersIdAndAddsOrFollowsTheDialog(t *testing.T) {
	lookup := `[{"foreignId":"stash:other","title":"A"},{"foreignId":"stash:wanted","title":"A"}]`
	base := map[string]string{"/api/v3/movie/lookup": lookup, "/api/v3/qualityprofile": `[{"id":6,"name":"Any"}]`, "/api/v3/rootfolder": `[{"path":"/adult"}]`}
	arr := &scriptedArr{answers: base}
	id, err := (ArrSync{}).companionScene(t.Context(), lidarrOver(arr).Whisparr(), "wanted", apiserver.AddedWork{Title: "A", Monitored: true, Search: true})
	if err != nil || id != 5 {
		t.Fatalf("id %d, error %v", id, err)
	}
	for _, want := range []string{"POST /api/v3/movie", `"foreignId":"stash:wanted"`, `"monitored":true`, `"searchForMovie":true`, `"qualityProfileId":6`, `"rootFolderPath":"/adult"`} {
		if !strings.Contains(arr.joined(), want) {
			t.Errorf("writes = %s, want %s", arr.joined(), want)
		}
	}
	if strings.Contains(arr.joined(), "stash:other") {
		t.Errorf("writes = %s: the other scene was added", arr.joined())
	}

	held := &scriptedArr{answers: map[string]string{"/api/v3/movie/lookup": `[{"id":9,"foreignId":"stash:wanted"}]`, "/api/v3/movie/9": `{"id":9}`}}
	if id, err := (ArrSync{}).companionScene(t.Context(), lidarrOver(held).Whisparr(), "wanted", apiserver.AddedWork{Title: "A", Monitored: true, Search: true}); err != nil || id != 9 {
		t.Fatalf("held: id %d, error %v", id, err)
	}
	for _, want := range []string{"PUT /api/v3/movie/9", `"movieIds":[9]`} {
		if !strings.Contains(held.joined(), want) {
			t.Errorf("held writes = %s, want %s", held.joined(), want)
		}
	}

	none := &scriptedArr{answers: map[string]string{"/api/v3/movie/lookup": `[{"foreignId":"stash:other"}]`}}
	if _, err := (ArrSync{}).companionScene(t.Context(), lidarrOver(none).Whisparr(), "wanted", apiserver.AddedWork{Title: "A", Monitored: true}); err == nil || none.joined() != "" {
		t.Errorf("error %v, writes %q; want an error and no write when no result has the id", err, none.joined())
	}
}

func TestMediaTarget_SendsABookNamedByGoodreadsToReadarrAndNoOtherBook(t *testing.T) {
	got, ok := mediaTarget(apiserver.AddedWork{Kind: "book", Provider: "goodreads", ExternalID: "b-5"})
	if !ok || got.app != settings.ArrAppReadarr || got.ref != "b-5" || got.linkKind != arrmap.LinkBook {
		t.Errorf("target = %+v, %v", got, ok)
	}
	if _, ok := mediaTarget(apiserver.AddedWork{Kind: "book", Provider: "openlibrary", ExternalID: "OL1W"}); ok {
		t.Error("a book named by another provider was sent to Readarr")
	}
}

func TestCompanionBook_AddsWhatReadarrLacksAndFollowsTheDialogForWhatItHolds(t *testing.T) {
	shelf := map[string]string{
		"/api/v1/author":      `[{"id":1,"qualityProfileId":2,"metadataProfileId":3,"rootFolderPath":"/shelf"}]`,
		"/api/v1/book/lookup": `[{"foreignBookId":"b-5","author":{"foreignAuthorId":"a-1"}}]`,
	}
	arr := &scriptedArr{answers: shelf}
	id, err := (ArrSync{}).companionBook(t.Context(), lidarrOver(arr), "b-5", apiserver.AddedWork{Monitored: true, Search: true})
	if err != nil || id != 5 {
		t.Fatalf("id %d, error %v", id, err)
	}
	for _, want := range []string{"POST /api/v1/book", `"monitored":true`, `"searchForNewBook":true`, `"qualityProfileId":2`, `"metadataProfileId":3`, `"rootFolderPath":"/shelf"`} {
		if !strings.Contains(arr.joined(), want) {
			t.Errorf("writes = %s, want %s", arr.joined(), want)
		}
	}

	held := &scriptedArr{answers: map[string]string{"/api/v1/book/lookup": `[{"id":7,"foreignBookId":"b-5"}]`, "/api/v1/book/7": `{"id":7}`}}
	if id, err := (ArrSync{}).companionBook(t.Context(), lidarrOver(held), "b-5", apiserver.AddedWork{Monitored: true, Search: true}); err != nil || id != 7 {
		t.Fatalf("held: id %d, error %v", id, err)
	}
	for _, want := range []string{"PUT /api/v1/book/7", `"name":"BookSearch"`, `"bookIds":[7]`} {
		if !strings.Contains(held.joined(), want) {
			t.Errorf("held writes = %s, want %s", held.joined(), want)
		}
	}
	quiet := &scriptedArr{answers: map[string]string{"/api/v1/book/lookup": `[{"id":7}]`}}
	if _, err := (ArrSync{}).companionBook(t.Context(), lidarrOver(quiet), "b-5", apiserver.AddedWork{}); err != nil || quiet.joined() != "" {
		t.Errorf("an unmonitored add wrote %q (%v), want nothing", quiet.joined(), err)
	}
	if _, err := (ArrSync{}).companionBook(t.Context(), lidarrOver(&scriptedArr{answers: map[string]string{}}), "b-5", apiserver.AddedWork{Monitored: true}); err == nil {
		t.Error("a lookup that finds nothing was not an error")
	}
}

// Readarr's books are sent back by the same rule as every other *Arr's items: only a linked book, and only Solarr's change.
func TestPusherBooks_SendsSolarrsMonitoringOfLinkedBooksToReadarrOnly(t *testing.T) {
	opened := newPushLibrary(t)
	ids, err := library.NewIdentity(opened).ResolveWorks(t.Context(), time.Unix(1_700_000_000, 0), []library.WorkLink{
		{Provider: "goodreads", ExternalID: "b-5", Confidence: 1, Work: library.WorkIdentity{MediaType: "book", CanonicalTitle: "The Quiet Pier", SortTitle: "Quiet Pier"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	desired, links := library.NewDesired(opened), library.NewArrLinks(opened)
	if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{WorkID: ids[0], CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	if err := links.Link(t.Context(), 5, library.ArrLink{SourceKind: arrmap.LinkBook, SourceID: "11", LocalKind: "work", LocalID: ids[0]}); err != nil {
		t.Fatal(err)
	}
	stored, err := links.ForInstance(t.Context(), 5)
	if err != nil {
		t.Fatal(err)
	}
	arr := &scriptedArr{answers: map[string]string{
		"/api/v1/book":    `[{"id":11,"monitored":false},{"id":12,"monitored":false}]`,
		"/api/v1/book/11": `{"id":11,"title":"Keep","monitored":false}`,
		"/api/v1/book/12": `{"id":12,"monitored":false}`,
	}}
	push := &pusher{
		sync: ArrSync{Desired: desired, Links: links}, instanceID: 5, client: lidarrOver(arr),
		links: map[string]map[string]library.ArrLink{arrmap.LinkBook: {stored[0].SourceID: stored[0]}},
	}
	if err := push.books(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(arr.writes) != 1 || !strings.HasPrefix(arr.writes[0], "PUT /api/v1/book/11 ") || !strings.Contains(arr.writes[0], `"monitored":true`) || !strings.Contains(arr.writes[0], `"title":"Keep"`) {
		t.Errorf("writes = %v, want only book 11 written, monitored true, the rest of it kept", arr.writes)
	}
	if push.result.Checked != 1 || push.result.Changed != 1 {
		t.Errorf("result = %+v, want one linked book checked and changed", push.result)
	}
}
