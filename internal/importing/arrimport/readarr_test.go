package arrimport_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
)

func TestReadarrReadsAuthorsAndBooksByTheirForeignIds(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{
		"/api/v1/system/status": `{"version":"0.4.0"}`,
		"/api/v1/rootfolder":    `[{"path":"/shelf/books"}]`,
		"/api/v1/author":        `[{"id":3,"authorName":"Ada Quill","foreignAuthorId":"a-77","monitored":true,"rootFolderPath":"/shelf/books","qualityProfileId":2,"metadataProfileId":1}]`,
		"/api/v1/book": `[{"id":11,"title":"The Quiet Pier","authorTitle":"Quill, Ada The Quiet Pier","foreignBookId":"b-5","foreignEditionId":"e-9","monitored":true,"releaseDate":"2019-04-02T00:00:00Z",
			"author":{"id":3,"authorName":"Ada Quill","foreignAuthorId":"a-77","rootFolderPath":"/shelf/books","qualityProfileId":2},"statistics":{"bookFileCount":1}},
			{"id":12,"title":"Undated","foreignBookId":"b-6","monitored":false}]`,
	}, puts: map[string]string{}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://readarr.local", "key").Readarr()
	if _, err := client.SystemStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	roots, err := client.RootFolders(t.Context())
	if err != nil || len(roots) != 1 || roots[0].Path != "/shelf/books" {
		t.Errorf("roots = %+v, %v", roots, err)
	}
	authors, err := client.Authors(t.Context())
	if err != nil || len(authors) != 1 || authors[0].ForeignAuthorID != "a-77" || !authors[0].Monitored || authors[0].MetadataProfileID != 1 {
		t.Errorf("authors = %+v, %v", authors, err)
	}
	books, err := client.Books(t.Context())
	if err != nil || len(books) != 2 {
		t.Fatalf("books = %+v, %v", books, err)
	}
	first := books[0]
	if first.ForeignBookID != "b-5" || first.ForeignEditionID != "e-9" || first.Year() != 2019 || first.Author.AuthorName != "Ada Quill" || first.Statistics.BookFileCount != 1 || first.Author.RootFolderPath != "/shelf/books" {
		t.Errorf("first book = %+v", first)
	}
	if books[1].Year() != 0 || books[1].Monitored {
		t.Errorf("second book = %+v, want no year and not monitored", books[1])
	}
}

func TestReadarrSetMonitoredWritesTheBookBackWithOnlyTheFlagChanged(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{"/api/v1/book/11": `{"id":11,"title":"Keep","monitored":false,"editions":[{"id":1}]}`}, puts: map[string]string{}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://readarr.local", "key").Readarr()
	if err := client.SetMonitored(t.Context(), "/api/v1/book", 11, true); err != nil {
		t.Fatal(err)
	}
	if got := transport.puts["/api/v1/book/11"]; got != `{"editions":[{"id":1}],"id":11,"monitored":true,"title":"Keep"}` {
		t.Errorf("put = %s", got)
	}
}
