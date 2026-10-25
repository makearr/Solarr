package googlebooks_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	body  string
	asked []string
}

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request.URL.String())
	status := http.StatusOK
	if a.body == "" {
		status = http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(a.body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper, key string) googlebooks.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return googlebooks.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "googlebooks", UserAgent: "test", Transport: transport}), func() string { return key })
}

const volume = `{"id":"abcDEF123","volumeInfo":{"title":"The Lantern Keeper","authors":["Mara Quell"],"publisher":"Northlight",
	"publishedDate":"1987-05-01","description":"A keeper of lanterns.","pageCount":312,"categories":["Fiction"],"language":"en",
	"industryIdentifiers":[{"type":"ISBN_10","identifier":"0000000000"},{"type":"ISBN_13","identifier":"9780000000002"}],
	"imageLinks":{"thumbnail":"http://books.google.com/t.jpg","small":"http://books.google.com/s.jpg"}}}`

func TestVolumeReadsTheBookAndItsBestPicture(t *testing.T) {
	transport := &answer{body: volume}
	found, err := client(t, transport, "").Volume(t.Context(), "abcDEF123")
	if err != nil {
		t.Fatal(err)
	}
	if found.ISBN() != "9780000000002" || found.Cover() != "https://books.google.com/s.jpg" || found.Year() != 1987 || found.VolumeInfo.PageCount != 312 {
		t.Errorf("volume = %+v", found)
	}
	if strings.Contains(transport.asked[0], "key=") {
		t.Errorf("no key should be sent without one: %s", transport.asked[0])
	}
}

func TestSearchAsksByTitleAndAuthorWithTheOwnersKey(t *testing.T) {
	transport := &answer{body: `{"items":[` + volume + `]}`}
	found, err := client(t, transport, "owner-key").Search(t.Context(), "The Lantern Keeper", "Mara Quell", 5)
	if err != nil || len(found) != 1 {
		t.Fatalf("found %v, %v", found, err)
	}
	asked := transport.asked[0]
	if !strings.Contains(asked, "q=intitle%3AThe+Lantern+Keeper+inauthor%3AMara+Quell") || !strings.Contains(asked, "key=owner-key") {
		t.Errorf("asked %s", asked)
	}
}

func TestByISBNAsksTheISBNAndAnEmptyAnswerIsNotFound(t *testing.T) {
	transport := &answer{body: `{"totalItems":0}`}
	if _, err := client(t, transport, "").ByISBN(t.Context(), "978-0000000002"); !errors.Is(err, googlebooks.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(transport.asked[0], "q=isbn%3A9780000000002") {
		t.Errorf("asked %s", transport.asked[0])
	}
	if _, err := client(t, transport, "").ByISBN(t.Context(), "not-an-isbn"); !errors.Is(err, googlebooks.ErrNotFound) || len(transport.asked) != 1 {
		t.Errorf("a malformed ISBN was asked: %v", transport.asked)
	}
}
