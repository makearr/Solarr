package indexer_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// capsFixtureXML is a representative t=caps response -- the shape shared by
// every real Newznab indexer this format was built from (NZBgeek, DOGnzb
// and similar), trimmed to what this package actually reads. <server>,
// <limits> and <registration> are real elements a live response also
// carries; they are omitted here because nothing in this package parses
// them, and Go's XML decoder ignores elements it has no struct field for.
const capsFixtureXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,imdbid,tvdbid"/>
    <movie-search available="yes" supportedParams="q,imdbid"/>
    <music-search available="yes" supportedParams="q,artist,album,label,track,year,genre"/>
    <book-search available="no" supportedParams="q,author,title"/>
    <audio-search available="no" supportedParams="q"/>
  </searching>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2010" name="Foreign"/>
      <subcat id="2040" name="HD"/>
    </category>
    <category id="3000" name="Audio">
      <subcat id="3010" name="MP3"/>
      <subcat id="3040" name="Lossless"/>
    </category>
  </categories>
</caps>`

// recordingTransport answers one canned response to every request, and
// records each request it saw -- the same shape internal/importing/arrimport's
// own fakeTransport uses, duplicated here rather than exported across
// packages for a test-only type.
type recordingTransport struct {
	status    int
	body      string
	requested []*http.Request
}

func (f *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requested = append(f.requested, request)
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    request,
	}, nil
}

// newTestClient builds an httpx.Client over transport -- a custom Transport
// bypasses the guard's own dialer entirely (Do still calls CheckURL, which
// only rejects a non-http(s) scheme), the same setup arrimport's own tests
// use for a connection this package does not actually reach over the
// network.
func newTestClient(t *testing.T, transport http.RoundTripper) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "indexer-test", UserAgent: "test", Transport: transport,
	})
}

func TestCapabilities_ParsesCategoriesAndSearchFunctions(t *testing.T) {
	transport := &recordingTransport{body: capsFixtureXML}
	client := indexer.NewClient(newTestClient(t, transport), "http://indexer.local", "test-api-key")

	caps, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}

	if len(transport.requested) != 1 {
		t.Fatalf("requested %d times, want 1", len(transport.requested))
	}
	query := transport.requested[0].URL.Query()
	if query.Get("t") != "caps" {
		t.Errorf("t = %q, want caps", query.Get("t"))
	}
	if query.Get("apikey") != "test-api-key" {
		t.Errorf("apikey = %q, want it sent", query.Get("apikey"))
	}

	if len(caps.Categories) != 2 {
		t.Fatalf("len(Categories) = %d, want 2", len(caps.Categories))
	}
	audio := caps.Categories[1]
	if audio.ID != 3000 || audio.Name != "Audio" {
		t.Errorf("Categories[1] = %+v, want id=3000 name=Audio", audio)
	}
	if len(audio.Subcategories) != 2 || audio.Subcategories[1].ID != 3040 || audio.Subcategories[1].Name != "Lossless" {
		t.Errorf("Audio.Subcategories = %+v, want [3010 MP3, 3040 Lossless]", audio.Subcategories)
	}

	music := caps.Search["music-search"]
	if !music.Available {
		t.Error("music-search.Available = false, want true")
	}
	wantParams := []string{"q", "artist", "album", "label", "track", "year", "genre"}
	if len(music.SupportedParams) != len(wantParams) {
		t.Fatalf("music-search.SupportedParams = %v, want %v", music.SupportedParams, wantParams)
	}
	for i, param := range wantParams {
		if music.SupportedParams[i] != param {
			t.Errorf("music-search.SupportedParams[%d] = %q, want %q", i, music.SupportedParams[i], param)
		}
	}

	book := caps.Search["book-search"]
	if book.Available {
		t.Error("book-search.Available = true, want false")
	}
}

// A public Torznab endpoint may need no API key at all (ADR-43/47) -- the
// caps request must not send an empty apikey parameter that was never
// configured.
func TestCapabilities_OmitsAPIKeyWhenNoneConfigured(t *testing.T) {
	transport := &recordingTransport{body: capsFixtureXML}
	client := indexer.NewClient(newTestClient(t, transport), "http://indexer.local", "")

	if _, err := client.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if _, present := transport.requested[0].URL.Query()["apikey"]; present {
		t.Error("apikey present in request, want it omitted entirely")
	}
}
