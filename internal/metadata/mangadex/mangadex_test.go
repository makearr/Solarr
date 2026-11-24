package mangadex_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/mangadex"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const entry = `{"id":"11111111-2222-3333-4444-555555555555","type":"manga","attributes":{"title":{"ja-ro":"Silent Tide","en":"The Silent Tide"},
"altTitles":[{"ja":"native"},{"en":"The Silent Tide"}],"description":{"en":"d"},"originalLanguage":"ja","status":"completed","year":2014,"lastVolume":"12",
"contentRating":"safe","publicationDemographic":"seinen","links":{"al":"31","mu":"8jqx4pm","mal":"801"},"tags":[{"attributes":{"name":{"en":"Drama"}}}]}}`

type answer struct{ requests *[]string }

func (a answer) RoundTrip(request *http.Request) (*http.Response, error) {
	*a.requests = append(*a.requests, request.URL.Path+"?"+request.URL.RawQuery)
	body := `{"result":"error"}`
	switch request.URL.Path {
	case "/manga":
		body = `{"result":"ok","data":[` + entry + `],"total":45}`
	case "/manga/11111111-2222-3333-4444-555555555555":
		body = `{"result":"ok","data":` + entry + `}`
	case "/manga/11111111-2222-3333-4444-555555555555/aggregate":
		body = `{"result":"ok","volumes":{"2":{"volume":"2","count":2,"chapters":{"10":{"chapter":"10"},"9":{"chapter":"9"}}},"1":{"volume":"1","chapters":{"1":{"chapter":"1"}}},
		 "none":{"volume":"none","chapters":{"11":{"chapter":"11"}}}}}`
	case "/cover":
		body = `{"result":"ok","data":[{"attributes":{"volume":"1","fileName":"a.jpg","locale":"ja"}},{"attributes":{"volume":"2","fileName":""}}]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newClient(requests *[]string) mangadex.Client {
	fake := clock.NewFakeAtEpoch()
	return mangadex.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "mangadex", UserAgent: "test", Transport: answer{requests}}))
}

func TestSearchAndMangaCarryLinksAndRating(t *testing.T) {
	var requests []string
	c := newClient(&requests)
	want := mangadex.Manga{ID: "11111111-2222-3333-4444-555555555555", Title: "The Silent Tide", AltTitles: []string{"Silent Tide", "native"}, Description: "d", OriginalLanguage: "ja",
		Status: "completed", Year: 2014, LastVolume: 12, ContentRating: "safe", Demographic: "seinen", Tags: []string{"Drama"}, Links: map[string]string{"al": "31", "mu": "8jqx4pm", "mal": "801"}}
	found, more, err := c.SearchManga(t.Context(), "silent tide", 1, []string{mangadex.RatingSafe, mangadex.RatingSuggestive})
	if err != nil || !more || len(found) != 1 || !reflect.DeepEqual(found[0], want) {
		t.Fatalf("found = %+v more=%v err=%v", found, more, err)
	}
	if !strings.Contains(requests[0], "contentRating%5B%5D=safe") || !strings.Contains(requests[0], "contentRating%5B%5D=suggestive") {
		t.Errorf("request %q does not carry the content ratings", requests[0])
	}
	one, err := c.Manga(t.Context(), want.ID)
	if err != nil || !reflect.DeepEqual(one, want) {
		t.Fatalf("manga = %+v, %v", one, err)
	}
}

func TestAggregateOrdersVolumesAndChaptersNumerically(t *testing.T) {
	var requests []string
	got, err := newClient(&requests).Aggregate(t.Context(), "11111111-2222-3333-4444-555555555555", "en")
	want := []mangadex.Volume{{Number: "1", Chapters: []string{"1"}}, {Number: "2", Chapters: []string{"9", "10"}}, {Number: mangadex.UnnumberedVolume, Chapters: []string{"11"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregate = %+v, %v", got, err)
	}
}

func TestCoversSkipEntriesWithoutAFile(t *testing.T) {
	var requests []string
	got, err := newClient(&requests).Covers(t.Context(), "11111111-2222-3333-4444-555555555555")
	want := []mangadex.Cover{{Volume: "1", Locale: "ja", URL: "https://uploads.mangadex.org/covers/11111111-2222-3333-4444-555555555555/a.jpg"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("covers = %+v, %v", got, err)
	}
}
