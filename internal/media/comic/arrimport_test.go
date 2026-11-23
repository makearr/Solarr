package comic_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/comic"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answerTransport map[string]string

func (a answerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, known := a[request.URL.String()]
	if !known {
		return nil, errors.New("no answer for " + request.URL.String())
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

type recordingAdding struct {
	mu       sync.Mutex
	requests []media.AddRequest
}

func (r *recordingAdding) LookupWorks(context.Context, media.AddLookup) ([]media.AddCandidate, error) {
	return nil, nil
}

func (r *recordingAdding) AddWork(_ context.Context, request media.AddRequest) (media.AddResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	return media.AddResult{WorkID: int64(len(r.requests)), Created: true}, nil
}

func TestManagerImportAddsEachSeriesByItsComicVineIdIntoTheLibraryOfItsFolder(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	transport := answerTransport{
		"http://mylar.local/api?apikey=k&cmd=getIndex": `[` +
			`{"ComicID":"18001","ComicName":"Night Riders","ComicYear":"2019","Status":"Active","ComicLocation":"/comics/Night Riders (2019)","Have":"3"},` +
			`{"ComicID":"","ComicName":"No Id","ComicYear":"2001","Status":"Active","ComicLocation":"/comics/No Id (2001)","Have":"0"},` +
			`{"ComicID":"18002","ComicName":"Elsewhere","ComicYear":"1990","Status":"Active","ComicLocation":"/other/Elsewhere (1990)","Have":"1"}]`,
	}
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{Provider: "mylar-test", UserAgent: "test", Transport: transport})
	adding := &recordingAdding{}
	var recorded []library.NewArrImportItem
	run := arrmap.Run{
		Client:  arrimport.NewLidarrClient(client, "http://mylar.local", "k"),
		Options: arrmap.Options{Monitored: true},
		Place: func(root string) (int64, bool) {
			return 7, root == "/comics"
		},
		Progress: func(int64, int64) {},
		Record: func(_ context.Context, item library.NewArrImportItem) error {
			recorded = append(recorded, item)
			return nil
		},
		Link: func(context.Context, string, int64, string, int64) {},
	}
	counts, err := comic.ManagerImport{App: settings.ArrAppMylar, Adding: adding}.Import(t.Context(), run)
	if err == nil || !strings.Contains(err.Error(), "/other: 1 items skipped") || !strings.Contains(err.Error(), "No Id: no ComicVine id") {
		t.Errorf("message = %v", err)
	}
	if counts.ArtistsCreated != 1 {
		t.Errorf("counts = %+v", counts)
	}
	want := media.AddRequest{
		CategoryID: 7, Kind: comic.AddKindSeries, Provider: comic.ProviderComicVine, ExternalID: "18001", Monitored: true,
		Fallback: &media.Fallback{Title: "Night Riders", Year: 2019, ExternalIDs: map[string]string{comic.ProviderComicVine: "18001"}},
	}
	if len(adding.requests) != 1 || adding.requests[0].CategoryID != want.CategoryID || adding.requests[0].ExternalID != "18001" ||
		adding.requests[0].Provider != want.Provider || !adding.requests[0].Monitored || adding.requests[0].Fallback.Title != "Night Riders" {
		t.Errorf("requests = %+v", adding.requests)
	}
	if len(recorded) != 1 || recorded[0].Kind != library.ArrImportItemSeries || recorded[0].SourceID != "18001" {
		t.Errorf("recorded = %+v", recorded)
	}
}
