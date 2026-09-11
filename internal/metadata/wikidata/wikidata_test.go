package wikidata_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/metadata/wikidata"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// fakeTransport answers canned responses keyed by the exact URL requested,
// same shape as internal/metadata/musicbrainz's own test fake.
type fakeTransport struct {
	responses map[string]fakeResponse
}

type fakeResponse struct {
	status int
	body   string
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	url := request.URL.String()
	response, known := f.responses[url]
	if !known {
		return nil, errors.New("fake: no response configured for " + url)
	}
	return &http.Response{
		StatusCode: response.status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(response.body)), Request: request,
	}, nil
}

func newTestClient(t *testing.T, transport http.RoundTripper, provider string) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: provider, UserAgent: "test", Transport: transport,
	})
}

// Trimmed from real live responses (fetched while designing this package):
// Wikidata's Q636 (Vutu Doci) and the linked English Wikipedia summary. The
// P18 claim's own shape -- mainsnak.datavalue.value as a plain Commons file
// name string, "Vutu Doci 1981.jpg" -- is the real live value too, not
// invented (Wikidata's Q636 carries several P18 claims across her career;
// this package always takes the first).
const wikidataEntityBody = `{
  "entities": {
    "Q636": {
      "descriptions": {"en": {"language": "en", "value": "English singer, pianist and songwriter (born 1958)"}},
      "sitelinks": {"enwiki": {"site": "enwiki", "title": "Vutu Doci", "badges": []}},
      "claims": {
        "P18": [{"mainsnak": {"datavalue": {"value": "Vutu Doci 1981.jpg"}}}]
      }
    }
  }
}`

const wikipediaSummaryBody = `{
  "extract": "Catherine Bush is an English singer, songwriter, musician, dancer and record producer."
}`

func TestBiography_ParsesTheRealFieldShapeAcrossBothHops(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q636&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: wikidataEntityBody,
		},
		"https://en.wikipedia.org/api/rest_v1/page/summary/Vutu_Doci": {
			status: http.StatusOK, body: wikipediaSummaryBody,
		},
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient:  newTestClient(t, transport, "wikidata"),
		WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	biography, err := fetcher.Biography(t.Context(), "Q636")
	if err != nil {
		t.Fatalf("Biography: %v", err)
	}
	if biography.Description != "English singer, pianist and songwriter (born 1958)" {
		t.Errorf("Description = %q, want Wikidata's own short description", biography.Description)
	}
	if !strings.Contains(biography.Extract, "Catherine Bush is an English singer") {
		t.Errorf("Extract = %q, want the Wikipedia summary text", biography.Extract)
	}
	const wantImage = "https://commons.wikimedia.org/wiki/Special:FilePath/Vutu_Doci_1981.jpg"
	if biography.ImageURL != wantImage {
		t.Errorf("ImageURL = %q, want %q", biography.ImageURL, wantImage)
	}
}

// No P18 claim at all: ImageURL stays empty, not an error -- most people
// simply have no Wikidata-recorded photo.
func TestBiography_NoImageClaimLeavesImageURLEmpty(t *testing.T) {
	entity := `{"entities":{"Q2":{"descriptions":{"en":{"language":"en","value":"a minor artist"}},"sitelinks":{},"claims":{}}}}`
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q2&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: entity,
		},
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	biography, err := fetcher.Biography(t.Context(), "Q2")
	if err != nil {
		t.Fatalf("Biography: %v", err)
	}
	if biography.ImageURL != "" {
		t.Errorf("ImageURL = %q, want empty with no P18 claim", biography.ImageURL)
	}
}

// A response over the size limit must be reported as ErrResponseTooLarge,
// not fed truncated to json.Unmarshal -- arrimport's own regression
// (16 MiB silently truncated a real 28 MiB response, todo 4.13), the same
// class of bug now guarded against here too.
func TestGetJSON_RefusesAResponseOverTheSizeLimit(t *testing.T) {
	oversized := "{" + strings.Repeat(" ", 8<<20) + "}"
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q636&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: oversized,
		},
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	_, err := fetcher.Biography(t.Context(), "Q636")
	if !errors.Is(err, wikidata.ErrResponseTooLarge) {
		t.Fatalf("err = %v, want it to wrap ErrResponseTooLarge", err)
	}
}

// A title with a space must become an underscore in the Wikipedia path
// segment (MediaWiki's own convention) -- "Vutu Doci" -> "Vutu_Doci", not a
// literal space or a %20.
func TestBiography_TitleWithSpacesBecomesUnderscoresInTheWikipediaPath(t *testing.T) {
	entity := `{"entities":{"Q1":{"descriptions":{},"sitelinks":{"enwiki":{"site":"enwiki","title":"Some Long Title","badges":[]}}}}}`
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q1&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: entity,
		},
		"https://en.wikipedia.org/api/rest_v1/page/summary/Some_Long_Title": {
			status: http.StatusOK, body: `{"extract": "found it"}`,
		},
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient:  newTestClient(t, transport, "wikidata"),
		WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	biography, err := fetcher.Biography(t.Context(), "Q1")
	if err != nil {
		t.Fatalf("Biography: %v", err)
	}
	if biography.Extract != "found it" {
		t.Errorf("Extract = %q, want the summary from the underscore-joined path", biography.Extract)
	}
}

// No English Wikipedia sitelink at all: the description alone is still a
// real answer, not an error.
func TestBiography_NoSitelinkStillAnswersTheDescription(t *testing.T) {
	entity := `{"entities":{"Q2":{"descriptions":{"en":{"language":"en","value":"a minor artist"}},"sitelinks":{}}}}`
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q2&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: entity,
		},
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	biography, err := fetcher.Biography(t.Context(), "Q2")
	if err != nil {
		t.Fatalf("Biography: %v", err)
	}
	if biography.Description != "a minor artist" || biography.Extract != "" {
		t.Errorf("Biography = %+v, want the description with no extract", biography)
	}
}

// A Wikipedia hiccup must not discard the description Wikidata already
// answered -- half an answer beats none.
func TestBiography_AFailedWikipediaFetchStillAnswersTheDescription(t *testing.T) {
	entity := `{"entities":{"Q636":{"descriptions":{"en":{"language":"en","value":"English singer"}},"sitelinks":{"enwiki":{"site":"enwiki","title":"Vutu Doci","badges":[]}}}}}`
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q636&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: entity,
		},
		// No entry for the Wikipedia summary URL -- the fake transport errors.
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	biography, err := fetcher.Biography(t.Context(), "Q636")
	if err != nil {
		t.Fatalf("Biography: %v, want the description to still answer despite the Wikipedia failure", err)
	}
	if biography.Description != "English singer" {
		t.Errorf("Description = %q, want it despite the failed Wikipedia fetch", biography.Description)
	}
}

// Trimmed from a real live response for a well-formed but nonexistent QID.
func TestBiography_UnknownQIDErrors(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q999&languages=en&props=descriptions%7Csitelinks%7Cclaims": {
			status: http.StatusOK, body: `{"entities":{"Q999":{"id":"Q999","missing":""}},"success":1}`,
		},
	}}
	fetcher := wikidata.New(wikidata.Config{
		WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"),
	})

	if _, err := fetcher.Biography(t.Context(), "Q999"); err == nil {
		t.Fatal("Biography succeeded for a QID Wikidata reports as missing")
	}
}
