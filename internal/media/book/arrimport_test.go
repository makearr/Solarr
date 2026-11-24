package book

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type readarrFake map[string]string

func (r readarrFake) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(r[request.URL.Path]))}, nil
}

func readarrClient(transport http.RoundTripper) arrimport.LidarrClient {
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return arrimport.NewLidarrClient(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "readarr-test", UserAgent: "test", Transport: transport,
	}), "http://readarr.local", "key")
}

type recordingAdding struct {
	mu       sync.Mutex
	requests []media.AddRequest
}

func (a *recordingAdding) LookupWorks(context.Context, media.AddLookup) ([]media.AddCandidate, error) {
	return nil, nil
}

func (a *recordingAdding) AddWork(_ context.Context, request media.AddRequest) (media.AddResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, request)
	return media.AddResult{WorkID: int64(len(a.requests)) + 100, Created: true}, nil
}

var readarrShelf = readarrFake{
	"/api/v1/system/status":          `{"version":"0.4.0"}`,
	"/api/v1/qualityprofile":         `[{"id":2,"name":"Epub"}]`,
	"/api/v1/rootfolder":             `[{"path":"/shelf/books"}]`,
	"/api/v1/config/mediamanagement": `{}`,
	"/api/v1/book": `[
	  {"id":11,"title":"The Quiet Pier","foreignBookId":"b-5","monitored":true,"releaseDate":"2019-04-02T00:00:00Z","author":{"authorName":"Ada Quill","rootFolderPath":"/shelf/books","qualityProfileId":2}},
	  {"id":12,"title":"Elsewhere","foreignBookId":"b-6","monitored":true,"author":{"authorName":"Bo Rook","rootFolderPath":"/elsewhere"}},
	  {"id":13,"title":"Unnamed","monitored":true,"author":{"rootFolderPath":"/shelf/books"}}]`,
}

func TestReadarrImportAddsEachMappedBookByItsGoodreadsIdWithReadarrsRecordAsFallback(t *testing.T) {
	adding := &recordingAdding{}
	var linked []string
	var mu sync.Mutex
	var recorded []library.NewArrImportItem
	run := arrmap.Run{
		Client: readarrClient(readarrShelf), Options: arrmap.Options{Monitored: true}, Progress: func(int64, int64) {},
		Place: func(root string) (int64, bool) { return 3, root == "/shelf/books" },
		Record: func(_ context.Context, item library.NewArrImportItem) error {
			mu.Lock()
			defer mu.Unlock()
			recorded = append(recorded, item)
			return nil
		},
		Link: func(_ context.Context, kind string, sourceID int64, localKind string, localID int64) {
			mu.Lock()
			defer mu.Unlock()
			linked = append(linked, fmt.Sprintf("%s %d %s %d", kind, sourceID, localKind, localID))
		},
	}
	counts, err := (ReadarrImport{Adding: map[string]media.Adding{"book": adding}}).Import(t.Context(), run)
	if err == nil || !strings.Contains(err.Error(), "no Goodreads id") || !strings.Contains(err.Error(), "/elsewhere") {
		t.Errorf("error = %v, want the unnamed book and the unmapped folder reported", err)
	}
	if len(adding.requests) != 1 {
		t.Fatalf("requests = %+v, want only the mapped, named book added", adding.requests)
	}
	want := media.AddRequest{
		CategoryID: 3, Provider: "goodreads", ExternalID: "b-5", Monitored: true,
		Fallback: &media.Fallback{Title: "The Quiet Pier", Year: 2019, Author: "Ada Quill"},
	}
	if !reflect.DeepEqual(adding.requests[0], want) {
		t.Errorf("request = %+v, want %+v", adding.requests[0], want)
	}
	if len(recorded) != 1 || recorded[0].Kind != library.ArrImportItemBook || recorded[0].SourceID != "11" || !recorded[0].WorkCreated {
		t.Errorf("recorded = %+v", recorded)
	}
	if !reflect.DeepEqual(linked, []string{"book 11 work 101"}) {
		t.Errorf("linked = %v, want the book linked for the push", linked)
	}
	_ = counts
}

func TestReadarrImportPreviewListsBooksWithTheirGoodreadsIds(t *testing.T) {
	source, err := (ReadarrImport{}).Preview(t.Context(), readarrClient(readarrShelf))
	if err != nil || source.ItemKind != "books" || source.MediaType != "book" || len(source.Items) != 3 {
		t.Fatalf("preview = %+v, %v", source, err)
	}
	if first := source.Items[0]; first.Provider != "goodreads" || first.ExternalID != "b-5" || first.RootFolder != "/shelf/books" || first.ProfileID != 2 || !first.Monitored {
		t.Errorf("first item = %+v", first)
	}
	if source.Profiles[2] != "Epub" || len(source.Roots) != 1 {
		t.Errorf("profiles %v roots %v", source.Profiles, source.Roots)
	}
}

func TestNamedIDsTakesAGoodreadsId(t *testing.T) {
	ids, ok := Ebook.NamedIDs("goodreads", " b-5 ")
	if !ok || ids != (IDs{Goodreads: "b-5"}) {
		t.Errorf("ids = %+v, %v", ids, ok)
	}
	if _, ok := Ebook.NamedIDs("goodreads", ""); ok {
		t.Error("an empty id was taken")
	}
}

func TestReadarrImportAddsToTheAudiobookCatalogForAnAudiobookLibrary(t *testing.T) {
	ebooks, audiobooks := &recordingAdding{}, &recordingAdding{}
	run := arrmap.Run{
		Client: readarrClient(readarrShelf), Options: arrmap.Options{Monitored: true}, Progress: func(int64, int64) {},
		Place:  func(root string) (int64, bool) { return 9, root == "/shelf/books" },
		Record: func(context.Context, library.NewArrImportItem) error { return nil },
		Link:   func(context.Context, string, int64, string, int64) {},
	}
	importer := ReadarrImport{
		Adding:      map[string]media.Adding{"book": ebooks, "audiobook": audiobooks},
		MediaTypeOf: func(categoryID int64) string { return map[int64]string{9: "audiobook"}[categoryID] },
	}
	if _, err := importer.Import(t.Context(), run); err == nil {
		t.Log("the unnamed book is reported, as before")
	}
	if len(audiobooks.requests) != 1 || audiobooks.requests[0].CategoryID != 9 || len(ebooks.requests) != 0 {
		t.Errorf("audiobook requests %+v, ebook requests %+v; want the one book in the audiobook catalog only", audiobooks.requests, ebooks.requests)
	}
}
