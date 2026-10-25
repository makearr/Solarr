package book

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Catalog is the book module's add and refresh over the service: media.Adding, with what a library's add dialog needs.
type Catalog struct {
	Service          Service
	Categories       Categories
	DefaultProfileID int64
	// AllowAgain lifts the exclusion of a book a person removed and now adds on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.Adding = Catalog{}

// LookupWorks implements media.Adding: the sources' books for a title, "Title by Author" or "Author - Title" narrowing
// it to a writer, each marked when the library already holds it.
func (c Catalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	query := strings.TrimSpace(lookup.Query)
	if len([]rune(query)) < 2 {
		return nil, media.ErrQueryTooShort
	}
	title, author := query, ""
	if info, ok := parsebook.Parse(query); ok && info.Author != "" && (strings.Contains(query, " by ") || strings.Contains(query, " - ")) {
		title, author = info.Title, info.Author
	}
	found, err := c.Service.Search(ctx, lookup.CategoryID, title, author, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	kind := c.Service.Format.MediaType
	candidates := make([]media.AddCandidate, 0, len(found))
	for _, book := range found {
		provider, externalID := c.Service.Format.CandidateID(book.IDs)
		if externalID == "" {
			continue
		}
		candidate := media.AddCandidate{
			Kind: kind, Provider: provider, ExternalID: externalID, Title: book.Title, Subtitle: book.Author,
			Overview: book.Overview, PosterURL: book.Cover, Year: int64(book.Year), Traits: book.Traits,
		}
		if id, held := c.Service.WorkByIDs(ctx, book.IDs); held {
			candidate.LibraryID = id
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// AddWork implements media.Adding: the book with Readarr's Monitor choice. It starts no search; the caller searches
// the book when asked to.
func (c Catalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	category, ok := c.Categories.Category(request.CategoryID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: no such library", media.ErrInvalidAdd)
	}
	format := c.Service.Format
	ids, ok := format.NamedIDs(request.Provider, request.ExternalID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, request.Provider, request.ExternalID)
	}
	monitor := MonitorBook
	if request.Option != "" {
		monitor = MonitorOption(request.Option)
	}
	if !request.Monitored {
		monitor = MonitorNone
	}
	if !monitor.Valid() {
		return media.AddResult{}, fmt.Errorf("%w: %q is not a monitor choice", media.ErrInvalidAdd, request.Option)
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	if request.ProfileID != nil {
		profile = *request.ProfileID
	}
	if c.AllowAgain != nil {
		if err := c.AllowAgain(ctx, request.Provider, request.ExternalID); err != nil {
			return media.AddResult{}, err
		}
	}
	var addedBy *int64
	if request.UserID != 0 {
		addedBy = &request.UserID
	}
	var fallback *Book
	if request.Fallback != nil {
		known := ids
		for provider, id := range request.Fallback.ExternalIDs {
			if more, ok := format.NamedIDs(provider, id); ok {
				known = Merge(Book{IDs: known}, Book{IDs: more}).IDs
			}
		}
		fallback = &Book{IDs: known, Title: request.Fallback.Title, Year: request.Fallback.Year}
	}
	result, err := c.Service.Add(ctx, AddRequest{
		IDs: ids, CategoryID: request.CategoryID, ProfileID: profile, Monitor: monitor, AddedBy: addedBy, Fallback: fallback,
	})
	if errors.Is(err, ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: format.MediaType, WorkID: result.WorkID, Created: result.Created}, nil
}

// Refresh re-reads one book from the sources; monitoring and the person's own fields stay as they are.
func (c Catalog) Refresh(ctx context.Context, categoryID, workID int64) error {
	_, err := c.Service.Refresh(ctx, categoryID, workID)
	if errors.Is(err, ErrNoSource) {
		return fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	return err
}

// Files is the book module's media.PlacingFiles: a queued file, or a whole folder of chapters, becomes the book a person
// names.
type Files struct {
	Resolver     Resolver
	Unidentified library.Unidentified
	Clock        clock.Clock
	// AllowAgain lifts the exclusion of a book a person names on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.PlacingFiles = Files{}

// PlaceFile implements media.PlacingFiles; with wholeFolder every other queued file beside it joins the same book.
func (f Files) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	ids, ok := f.Resolver.Service.Format.NamedIDs(placement.Provider, placement.ExternalID)
	if !ok {
		return 0, 0, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, placement.Provider, placement.ExternalID)
	}
	if f.AllowAgain != nil {
		if err := f.AllowAgain(ctx, placement.Provider, placement.ExternalID); err != nil {
			return 0, 0, err
		}
	}
	workID := f.Resolver.ByIDs(ctx, categoryID, ids)
	if workID <= 0 {
		return 0, 0, fmt.Errorf("%w: that book could not be added", media.ErrInvalidAdd)
	}
	entry, err := f.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	files := []int64{mediaObjectID}
	if wholeFolder || f.Resolver.Service.Format.ByFolder {
		if files, err = f.Unidentified.InFolder(ctx, entry.RootFolderID, entry.RelPath); err != nil {
			return 0, 0, err
		}
	}
	now := f.Clock.Now()
	if _, err := f.Resolver.Service.Series.AttachWholeFiles(ctx, now, workID, entry.RootFolderID, files); err != nil {
		return workID, 0, err
	}
	for _, id := range files {
		if err := f.Unidentified.RecordManual(ctx, id, workID, now.Unix()); err != nil {
			return workID, 0, err
		}
	}
	if err := f.Unidentified.Clear(ctx, files); err != nil {
		return workID, 0, err
	}
	return workID, int64(len(files)), nil
}
