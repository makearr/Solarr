package book

import (
	"context"
	"errors"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
)

// ReadarrImport adds Readarr's books, four at once, each by the Goodreads work id Readarr names it by. A book Solarr
// already holds is matched and left as it is; one it creates takes Readarr's monitored flag and the quality profile
// the import's mapping names. Readarr's own record (title, author, year) stands in when no source knows the book.
type ReadarrImport struct {
	// Adding is each book media type's add, by type: the library a book's root folder belongs to decides which.
	Adding   map[string]media.Adding
	Cache    *settings.Cache
	Monitors arrmap.Monitors
}

// Import walks Readarr's books.
func (r ReadarrImport) Import(ctx context.Context, run arrmap.Run) (library.ArrImportCounts, error) {
	return arrmap.Walk(ctx, run, r.source(run))
}

func (r ReadarrImport) source(run arrmap.Run) arrmap.Source[arrimport.ReadarrBook] {
	client := run.Client.Readarr()
	var profileNames map[int64]string
	return arrmap.Source[arrimport.ReadarrBook]{
		Connect: func(ctx context.Context) error {
			if len(r.Adding) == 0 {
				return errors.New("no book service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		List: func(ctx context.Context) ([]arrimport.ReadarrBook, error) {
			profileNames = readarrProfileNames(ctx, client)
			return client.Books(ctx)
		},
		Describe: func(book arrimport.ReadarrBook) arrmap.Item {
			return arrmap.Item{Name: book.Title, RootFolder: book.Author.RootFolderPath, Monitored: book.Monitored, HasFiles: book.Statistics.BookFileCount > 0}
		},
		Workers: 4,
		Monitor: func(ctx context.Context, categoryID int64, book arrimport.ReadarrBook) (library.ArrImportCounts, error) {
			workID, held, err := r.Monitors.Work(ctx, categoryID, mediaTypeOf(r.Cache, categoryID), ProviderGoodreads, book.ForeignBookID, book.Monitored)
			if held && err == nil {
				run.Link(ctx, arrmap.LinkBook, book.ID, "work", workID)
			}
			return arrmap.Found(held, err)
		},
		Sync: func(ctx context.Context, categoryID int64, book arrimport.ReadarrBook) (library.ArrImportCounts, error) {
			return arrmap.Tally(r.syncBook(ctx, run, categoryID, book, profileNames))
		},
	}
}

// mediaTypeOf is the media type of the library an import item belongs to: books or audiobooks.
func mediaTypeOf(cache *settings.Cache, categoryID int64) string {
	if cache != nil {
		if category, ok := cache.Snapshot().Category(categoryID); ok {
			return category.MediaType
		}
	}
	return Ebook.MediaType
}

func readarrProfileNames(ctx context.Context, client arrimport.ReadarrClient) map[int64]string {
	return arrmap.ProfileNames(ctx, client.QualityProfiles, func(profile arrimport.SonarrQualityProfile) (int64, string) {
		return profile.ID, profile.Name
	})
}

func (r ReadarrImport) syncBook(ctx context.Context, run arrmap.Run, categoryID int64, book arrimport.ReadarrBook, profileNames map[int64]string) (bool, error) {
	if book.ForeignBookID == "" {
		return false, errors.New("no Goodreads id")
	}
	request := media.AddRequest{
		CategoryID: categoryID, Provider: ProviderGoodreads, ExternalID: book.ForeignBookID, Monitored: book.Monitored,
		Fallback: &media.Fallback{Title: book.Title, Year: book.Year(), Author: book.Author.AuthorName},
	}
	if profile, ok := r.bookProfile(categoryID, book, profileNames, run.Options.ProfileMap); ok {
		request.ProfileID = &profile
	}
	adding, known := r.Adding[mediaTypeOf(r.Cache, categoryID)]
	if !known {
		return false, errors.New("no book service for this library")
	}
	result, err := adding.AddWork(ctx, request)
	if err != nil {
		return false, err
	}
	workID := result.WorkID
	if err := run.Record(ctx, library.NewArrImportItem{
		Kind: library.ArrImportItemBook, SourceID: strconv.FormatInt(book.ID, 10), Title: book.Title, WorkID: &workID, WorkCreated: result.Created,
	}); err != nil {
		return false, err
	}
	run.Link(ctx, arrmap.LinkBook, book.ID, "work", workID)
	return result.Created, nil
}

// bookProfile is the Solarr profile the import's mapping names for the book's Readarr profile, else the one of the same
// name; none leaves the library's own.
func (r ReadarrImport) bookProfile(categoryID int64, book arrimport.ReadarrBook, profileNames map[int64]string, profileMap map[int64]*int64) (int64, bool) {
	id, mapped := profileMap[book.Author.QualityProfileID]
	if !mapped && r.Cache != nil {
		id = arrmap.SameNameProfile(r.Cache.Snapshot().QualityProfiles(), mediaTypeOf(r.Cache, categoryID), profileNames[book.Author.QualityProfileID])
	}
	if id == nil {
		return 0, false
	}
	return *id, true
}

// Preview reads Readarr's books.
func (r ReadarrImport) Preview(ctx context.Context, client arrimport.LidarrClient) (arrmap.PreviewSource, error) {
	readarr := client.Readarr()
	source := arrmap.PreviewSource{ItemKind: "books", MediaType: Ebook.MediaType}
	books, err := readarr.Books(ctx)
	if err != nil {
		return arrmap.PreviewSource{}, err
	}
	for _, book := range books {
		source.Items = append(source.Items, arrmap.PreviewItem{
			RootFolder: book.Author.RootFolderPath, Monitored: book.Monitored, ProfileID: book.Author.QualityProfileID,
			Provider: ProviderGoodreads, ExternalID: book.ForeignBookID,
		})
	}
	return arrmap.ReadPreview(ctx, source, readarr, readarrProfileNames(ctx, readarr))
}
