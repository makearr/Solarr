package mdblist_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{ asked []string }

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request.URL.String())
	status, body := http.StatusNotFound, `{"error":"Not found"}`
	if strings.Contains(request.URL.Path, "/tmdb/movie/7001/") {
		status, body = http.StatusOK, `{"title":"Example Film","year":1999,"imdb_id":"tt9000001","ratings":[
		  {"source":"imdb","value":8.1,"score":81,"votes":1200},{"source":"tomatoes","value":97,"score":97,"votes":null},
		  {"source":"letterboxd","value":null,"score":null,"votes":null}]}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "mdblist", UserAgent: "test", Transport: transport})
}

func TestAFilmsRatingsAreReadByTMDBIDWithTheOwnersKey(t *testing.T) {
	transport := &answer{}
	ratings := mdblist.New(client(t, transport), func() string { return "owner-key" })
	media, err := ratings.Movie(t.Context(), 7001, "")
	if err != nil {
		t.Fatal(err)
	}
	want := mdblist.Media{Title: "Example Film", Year: 1999, IMDbID: "tt9000001", Ratings: []mdblist.Rating{
		{Source: "imdb", Value: 8.1, Score: 81, Votes: 1200}, {Source: "tomatoes", Value: 97, Score: 97}, {Source: "letterboxd"},
	}}
	if !reflect.DeepEqual(media, want) {
		t.Errorf("media = %+v, want %+v", media, want)
	}
	if !strings.Contains(transport.asked[0], "apikey=owner-key") {
		t.Errorf("asked %s; want the owner's key", transport.asked[0])
	}
	if _, err := ratings.Movie(t.Context(), 1, ""); !errors.Is(err, mdblist.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound for a film MDBList does not know", err)
	}
	if _, err := mdblist.New(client(t, transport), func() string { return "" }).Movie(t.Context(), 7001, ""); !errors.Is(err, mdblist.ErrNotFound) || len(transport.asked) != 2 {
		t.Errorf("without a key nothing is asked: %v, %d requests", err, len(transport.asked))
	}
}

type listAnswer struct{ asked []string }

func (a *listAnswer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request.URL.String())
	status, body := http.StatusNotFound, `{"error":"List Not found"}`
	if strings.HasSuffix(request.URL.Path, "/lists/someone/shows/items") || strings.HasSuffix(request.URL.Path, "/lists/42/items") {
		status, body = http.StatusOK, `{"movies":[],"shows":[{"title":"Example Show","release_year":2021,"ids":{"mdblist":"abc","imdb":"tt1","tmdb":5,"tvdb":null}}],"pagination":{"has_more":true}}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestAListIsReadByNumberOrByUserAndName(t *testing.T) {
	transport := &listAnswer{}
	lists := mdblist.New(client(t, transport), func() string { return "owner-key" })
	for _, ref := range []string{"42", "someone/shows"} {
		page, err := lists.ListItems(t.Context(), ref, 100, 100)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		want := mdblist.ListItem{Title: "Example Show", Year: 2021, IDs: mdblist.ListIDs{MDBList: "abc", IMDb: "tt1", TMDB: 5}}
		if len(page.Shows) != 1 || page.Shows[0] != want || !page.Pagination.HasMore {
			t.Errorf("%s: page = %+v, want one show %+v and more to come", ref, page, want)
		}
	}
	if !strings.Contains(transport.asked[0], "offset=100") || !strings.Contains(transport.asked[0], "limit=100") || !strings.Contains(transport.asked[0], "apikey=owner-key") {
		t.Errorf("asked %q, want the offset, limit and key", transport.asked[0])
	}
	if _, err := lists.ListItems(t.Context(), "nobody/none", 0, 100); !errors.Is(err, mdblist.ErrNotFound) {
		t.Errorf("a missing list = %v, want ErrNotFound", err)
	}
	if _, err := mdblist.New(client(t, transport), func() string { return "" }).ListItems(t.Context(), "42", 0, 100); !errors.Is(err, mdblist.ErrNoKey) {
		t.Errorf("without a key = %v, want ErrNoKey", err)
	}
}
