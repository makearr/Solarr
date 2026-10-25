package openlibrary_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	bodies map[string]string
	asked  []string
}

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request.URL.String())
	status, body := http.StatusNotFound, `{"error":"notfound"}`
	if found, ok := a.bodies[request.URL.Path]; ok {
		status, body = http.StatusOK, found
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper) openlibrary.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return openlibrary.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "openlibrary", UserAgent: "test", Transport: transport}))
}

func TestSearchAsksByTitleAndAuthorAndReadsTheDocs(t *testing.T) {
	transport := &answer{bodies: map[string]string{"/search.json": `{"numFound":1,"docs":[{"key":"/works/OL1001W","title":"The Lantern Keeper",
		"author_name":["Mara Quell"],"author_key":["OL2001A"],"first_publish_year":1987,"isbn":["9780000000002"],"cover_i":42,
		"language":["eng"],"number_of_pages_median":312,"id_goodreads":["77"]}]}`}}
	docs, err := client(t, transport).Search(t.Context(), "The Lantern Keeper", "Mara Quell", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []openlibrary.Doc{{Key: "/works/OL1001W", Title: "The Lantern Keeper", AuthorNames: []string{"Mara Quell"}, AuthorKeys: []string{"OL2001A"},
		FirstPublishYear: 1987, ISBN: []string{"9780000000002"}, CoverID: 42, Languages: []string{"eng"}, Pages: 312, Goodreads: []string{"77"}}}
	if !reflect.DeepEqual(docs, want) {
		t.Errorf("docs = %+v, want %+v", docs, want)
	}
	if docs[0].WorkID() != "OL1001W" {
		t.Errorf("work id = %q", docs[0].WorkID())
	}
	if asked := transport.asked[0]; !strings.Contains(asked, "author=Mara+Quell") || !strings.Contains(asked, "title=The+Lantern+Keeper") || !strings.Contains(asked, "limit=5") {
		t.Errorf("asked %s", asked)
	}
}

func TestSearchByAnISBNAsksTheISBNIndex(t *testing.T) {
	transport := &answer{bodies: map[string]string{"/search.json": `{"docs":[]}`}}
	if _, err := client(t, transport).Search(t.Context(), "978-0-00-000000-2", "", 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transport.asked[0], "isbn=9780000000002") {
		t.Errorf("asked %s", transport.asked[0])
	}
}

func TestWorkReadsADescriptionInEitherShape(t *testing.T) {
	for _, description := range []string{`"A keeper of lanterns."`, `{"type":"/type/text","value":"A keeper of lanterns."}`} {
		transport := &answer{bodies: map[string]string{"/works/OL1001W.json": `{"key":"/works/OL1001W","title":"The Lantern Keeper",
			"description":` + description + `,"covers":[42,-1],"first_publish_date":"1987","authors":[{"author":{"key":"/authors/OL2001A"}},{"author":{"key":"/x"}}]}`}}
		work, err := client(t, transport).Work(t.Context(), "OL1001W")
		if err != nil {
			t.Fatal(err)
		}
		if work.Description != "A keeper of lanterns." || !reflect.DeepEqual(work.AuthorIDs(), []string{"OL2001A"}) || work.FirstPublished != "1987" {
			t.Errorf("work = %+v", work)
		}
	}
}

func TestAnUnknownOrMalformedIDIsNotFound(t *testing.T) {
	transport := &answer{}
	if _, err := client(t, transport).Work(t.Context(), "OL9W"); !errors.Is(err, openlibrary.ErrNotFound) {
		t.Errorf("unknown work: %v", err)
	}
	if _, err := client(t, transport).Author(t.Context(), "../etc"); !errors.Is(err, openlibrary.ErrNotFound) {
		t.Errorf("malformed author: %v", err)
	}
	if len(transport.asked) != 1 {
		t.Errorf("a malformed id was asked: %v", transport.asked)
	}
}

func TestByISBNNamesTheEditionsWork(t *testing.T) {
	transport := &answer{bodies: map[string]string{"/isbn/9780000000002.json": `{"key":"/books/OL3001M","title":"The Lantern Keeper",
		"works":[{"key":"/works/OL1001W"}],"publishers":["Northlight"],"isbn_13":["9780000000002"]}`}}
	edition, err := client(t, transport).ByISBN(t.Context(), "978-0000000002")
	if err != nil {
		t.Fatal(err)
	}
	if edition.WorkID() != "OL1001W" || edition.Publishers[0] != "Northlight" {
		t.Errorf("edition = %+v", edition)
	}
}

func TestCoverAndPhotoAddresses(t *testing.T) {
	if got := openlibrary.CoverURL(42); got != "https://covers.openlibrary.org/b/id/42-L.jpg" {
		t.Errorf("cover = %s", got)
	}
	if openlibrary.CoverURL(-1) != "" || openlibrary.AuthorPhotoURL("x") != "" {
		t.Error("no picture should be no address")
	}
}
